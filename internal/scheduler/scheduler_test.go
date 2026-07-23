package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// --- infraestrutura de teste --------------------------------------------------

// fonteFixa devolve sempre a mesma lista de itens (a fila "no banco" simulada). O
// scheduler filtra por conta propria (rodando/concluidas/naoAntesDe), entao dar a
// lista completa a cada Prontas exercita justamente esses filtros.
type fonteFixa struct {
	itens []Item
	erro  error
}

func (f *fonteFixa) Prontas(context.Context) ([]Item, error) { return f.itens, f.erro }

// relogio e um relogio controlavel para os testes de reagendamento por franquia.
type relogio struct {
	mu sync.Mutex
	t  time.Time
}

func novoRelogio() *relogio {
	return &relogio{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}
func (r *relogio) agora() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.t
}
func (r *relogio) avancar(d time.Duration) {
	r.mu.Lock()
	r.t = r.t.Add(d)
	r.mu.Unlock()
}

// stubExec registra concorrencia (global e por projeto), contas vistas por
// demanda e quantas vezes cada demanda rodou. O desfecho de cada demanda e
// decidido por `desfecho` (default: Concluido). `trabalho` segura o worker por um
// tempo para criar contencao real sob carga.
type stubExec struct {
	trabalho time.Duration
	desfecho func(item Item, execN int) (Desfecho, error)

	mu             sync.Mutex
	emCurso        int
	pico           int
	porProjeto     map[int64]int
	picoPorProjeto map[int64]int
	contaEmUso     map[string]int
	picoPorConta   map[string]int
	contasDe       map[int64]map[string]bool
	execN          map[int64]int
}

func novoStub() *stubExec {
	return &stubExec{
		porProjeto:     map[int64]int{},
		picoPorProjeto: map[int64]int{},
		contaEmUso:     map[string]int{},
		picoPorConta:   map[string]int{},
		contasDe:       map[int64]map[string]bool{},
		execN:          map[int64]int{},
	}
}

func (e *stubExec) Executar(ctx context.Context, item Item, conta string) (Desfecho, error) {
	e.mu.Lock()
	e.emCurso++
	if e.emCurso > e.pico {
		e.pico = e.emCurso
	}
	e.porProjeto[item.ProjectID]++
	if e.porProjeto[item.ProjectID] > e.picoPorProjeto[item.ProjectID] {
		e.picoPorProjeto[item.ProjectID] = e.porProjeto[item.ProjectID]
	}
	if conta != "" {
		e.contaEmUso[conta]++
		if e.contaEmUso[conta] > e.picoPorConta[conta] {
			e.picoPorConta[conta] = e.contaEmUso[conta]
		}
		if e.contasDe[item.DemandaID] == nil {
			e.contasDe[item.DemandaID] = map[string]bool{}
		}
		e.contasDe[item.DemandaID][conta] = true
	}
	e.execN[item.DemandaID]++
	n := e.execN[item.DemandaID]
	e.mu.Unlock()

	if e.trabalho > 0 {
		select {
		case <-time.After(e.trabalho):
		case <-ctx.Done():
		}
	}

	e.mu.Lock()
	e.emCurso--
	e.porProjeto[item.ProjectID]--
	if conta != "" {
		e.contaEmUso[conta]--
	}
	e.mu.Unlock()

	if e.desfecho != nil {
		return e.desfecho(item, n)
	}
	return Desfecho{Concluido: true}, nil
}

func (e *stubExec) picoGlobal() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pico
}
func (e *stubExec) picoProjeto(p int64) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.picoPorProjeto[p]
}
func (e *stubExec) execCount(d int64) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.execN[d]
}
func (e *stubExec) contasDemanda(d int64) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var cs []string
	for c := range e.contasDe[d] {
		cs = append(cs, c)
	}
	return cs
}
func (e *stubExec) picoConta(c string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.picoPorConta[c]
}

// rodarPor sobe o scheduler em background e o encerra apos a condicao (ou o
// timeout). Devolve quando o loop e os workers ja terminaram.
func rodarPor(t *testing.T, s *Scheduler, cond func() bool, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	fim := make(chan struct{})
	go func() { s.Rodar(ctx); close(fim) }()

	prazo := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(prazo) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	select {
	case <-fim:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler nao encerrou a tempo")
	}
}

func itens(n int, projeto int64) []Item {
	is := make([]Item, n)
	for i := range is {
		is[i] = Item{DemandaID: int64(i + 1), ProjectID: projeto}
	}
	return is
}

// --- testes -------------------------------------------------------------------

// TestReenfileirarReativaDemandaConcluida: uma demanda que concluiu (sucesso ou
// falha) ganha a marca permanente `concluidas` e nunca mais é despachada neste
// processo. Reenfileirar (ação tentar_novamente) limpa a marca e a demanda volta
// a rodar quando a Fonte a listar de novo.
func TestReenfileirarReativaDemandaConcluida(t *testing.T) {
	stub := novoStub()
	is := []Item{{DemandaID: 1, ProjectID: 1}}
	s := Novo(Opcoes{Fonte: &fonteFixa{itens: is}, Executor: stub, Intervalo: 2 * time.Millisecond})

	// 1ª execução: conclui e ganha a marca de concluída.
	rodarPor(t, s, func() bool { return stub.execCount(1) >= 1 }, 3*time.Second)
	if got := stub.execCount(1); got != 1 {
		t.Fatalf("demanda rodou %d vez(es), esperava 1", got)
	}

	// sem Reenfileirar, o dispatch é vetado pela marca.
	if s.tentarDespachar(context.Background(), is[0]) {
		t.Fatal("demanda concluída não deveria ser despachada de novo")
	}

	// Reenfileirar limpa a marca; a demanda volta a ser despachável.
	s.Reenfileirar(1)
	rodarPor(t, s, func() bool { return stub.execCount(1) >= 2 }, 3*time.Second)
	if got := stub.execCount(1); got < 2 {
		t.Fatalf("demanda rodou %d vez(es) após Reenfileirar, esperava 2", got)
	}
}

func TestLimiteGlobalRespeitado(t *testing.T) {
	stub := novoStub()
	stub.trabalho = 8 * time.Millisecond
	is := itens(12, 1)
	s := Novo(Opcoes{
		Fonte:         &fonteFixa{itens: is},
		Executor:      stub,
		MaxGlobal:     3,
		MaxPorProjeto: 0, // sem limite por projeto: so o global segura
		Intervalo:     2 * time.Millisecond,
	})
	rodarPor(t, s, func() bool {
		concluidas := 0
		for _, it := range is {
			if stub.execCount(it.DemandaID) > 0 {
				concluidas++
			}
		}
		return concluidas == len(is)
	}, 3*time.Second)

	if p := stub.picoGlobal(); p > 3 {
		t.Fatalf("pico de concorrencia global = %d, deveria respeitar o limite 3", p)
	}
	for _, it := range is {
		if stub.execCount(it.DemandaID) != 1 {
			t.Fatalf("demanda %d rodou %d vezes, esperava 1", it.DemandaID, stub.execCount(it.DemandaID))
		}
	}
}

func TestLimitePorProjetoRespeitado(t *testing.T) {
	stub := novoStub()
	stub.trabalho = 8 * time.Millisecond
	// duas demandas por projeto, tres projetos.
	is := []Item{
		{DemandaID: 1, ProjectID: 10}, {DemandaID: 2, ProjectID: 10},
		{DemandaID: 3, ProjectID: 20}, {DemandaID: 4, ProjectID: 20},
		{DemandaID: 5, ProjectID: 30}, {DemandaID: 6, ProjectID: 30},
	}
	s := Novo(Opcoes{
		Fonte:         &fonteFixa{itens: is},
		Executor:      stub,
		MaxGlobal:     10, // global folgado: o limite efetivo e o por projeto
		MaxPorProjeto: 1,
		Intervalo:     2 * time.Millisecond,
	})
	rodarPor(t, s, func() bool {
		for _, it := range is {
			if stub.execCount(it.DemandaID) == 0 {
				return false
			}
		}
		return true
	}, 3*time.Second)

	for _, p := range []int64{10, 20, 30} {
		if pk := stub.picoProjeto(p); pk > 1 {
			t.Fatalf("projeto %d teve %d execucoes simultaneas, limite era 1", p, pk)
		}
	}
	// com 3 projetos a 1 cada, o paralelismo global deve ter chegado a >1 (senao
	// o teste nao provou concorrencia entre projetos).
	if stub.picoGlobal() < 2 {
		t.Fatalf("pico global = %d; esperava concorrencia entre projetos", stub.picoGlobal())
	}
}

func TestAfinidadeContaDemanda(t *testing.T) {
	stub := novoStub()
	stub.trabalho = 6 * time.Millisecond
	// cada demanda roda 3 vezes (nao conclui nas 2 primeiras) para provar que
	// mantem a MESMA conta a cada redespacho.
	stub.desfecho = func(item Item, n int) (Desfecho, error) {
		return Desfecho{Concluido: n >= 3}, nil
	}
	is := itens(4, 1)
	s := Novo(Opcoes{
		Fonte:         &fonteFixa{itens: is},
		Executor:      stub,
		MaxGlobal:     10,
		MaxPorProjeto: 0,
		Contas:        []string{"contaA", "contaB"},
		Intervalo:     2 * time.Millisecond,
	})
	rodarPor(t, s, func() bool {
		for _, it := range is {
			if stub.execCount(it.DemandaID) < 3 {
				return false
			}
		}
		return true
	}, 5*time.Second)

	// cada demanda sempre usou uma unica conta (afinidade).
	for _, it := range is {
		cs := stub.contasDemanda(it.DemandaID)
		if len(cs) != 1 {
			t.Fatalf("demanda %d usou contas %v, esperava exatamente 1 (afinidade)", it.DemandaID, cs)
		}
	}
	// nenhuma conta rodou duas demandas ao mesmo tempo (exclusividade da conta),
	// logo o paralelismo maximo = numero de contas (2).
	for _, c := range []string{"contaA", "contaB"} {
		if pk := stub.picoConta(c); pk > 1 {
			t.Fatalf("conta %s teve %d usos simultaneos, deveria ser exclusiva (<=1)", c, pk)
		}
	}
	if stub.picoGlobal() > 2 {
		t.Fatalf("pico global = %d; com 2 contas exclusivas o teto e 2", stub.picoGlobal())
	}
}

func TestReagendamentoPorFranquiaNaoBloqueia(t *testing.T) {
	rel := novoRelogio()
	stub := novoStub()
	// demanda 1 = franquia: na 1a rodada devolve RetomarEm no futuro (sem dormir,
	// sem concluir); depois de o relogio avancar, conclui. Demais concluem de cara.
	stub.desfecho = func(item Item, n int) (Desfecho, error) {
		if item.DemandaID == 1 && n == 1 {
			return Desfecho{RetomarEm: rel.agora().Add(10 * time.Minute)}, nil
		}
		return Desfecho{Concluido: true}, nil
	}
	is := itens(4, 1)
	s := Novo(Opcoes{
		Fonte:         &fonteFixa{itens: is},
		Executor:      stub,
		MaxGlobal:     1, // 1 worker: se a franquia bloqueasse, ninguem mais rodaria
		MaxPorProjeto: 0,
		Intervalo:     2 * time.Millisecond,
		Agora:         rel.agora,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fim := make(chan struct{})
	go func() { s.Rodar(ctx); close(fim) }()
	defer func() {
		cancel()
		select {
		case <-fim:
		case <-time.After(5 * time.Second):
			t.Fatal("scheduler nao encerrou a tempo")
		}
	}()

	// as outras 3 demandas devem concluir mesmo com a franquia "esperando" (prova
	// que o unico worker nao ficou preso na demanda 1).
	if !esperar(func() bool {
		return stub.execCount(2) == 1 && stub.execCount(3) == 1 && stub.execCount(4) == 1
	}, 3*time.Second) {
		t.Fatalf("demandas 2..4 nao concluiram enquanto a franquia esperava (worker bloqueado?)")
	}

	// a demanda 1 rodou uma vez (franquia) e NAO deve rodar de novo antes do
	// horario de retomada.
	if stub.execCount(1) != 1 {
		t.Fatalf("demanda 1 rodou %d vezes antes do reset; esperava 1", stub.execCount(1))
	}
	time.Sleep(30 * time.Millisecond) // ticks passam; nao pode redespachar ainda
	if stub.execCount(1) != 1 {
		t.Fatalf("demanda 1 foi redespachada antes de RetomarEm (%d execucoes)", stub.execCount(1))
	}

	// avanca o relogio para depois do reset: a demanda 1 deve ser retomada e concluir.
	rel.avancar(11 * time.Minute)
	if !esperar(func() bool { return stub.execCount(1) == 2 }, 3*time.Second) {
		t.Fatalf("demanda 1 nao foi retomada apos o reset da franquia (execN=%d)", stub.execCount(1))
	}
}

func TestErroDeInfraReagendaComBackoff(t *testing.T) {
	rel := novoRelogio()
	stub := novoStub()
	// sempre erra na 1a rodada; conclui na 2a.
	stub.desfecho = func(item Item, n int) (Desfecho, error) {
		if n == 1 {
			return Desfecho{}, errors.New("banco indisponivel")
		}
		return Desfecho{Concluido: true}, nil
	}
	is := itens(1, 1)
	s := Novo(Opcoes{
		Fonte:       &fonteFixa{itens: is},
		Executor:    stub,
		MaxGlobal:   2,
		Intervalo:   2 * time.Millisecond,
		BackoffErro: 5 * time.Minute,
		Agora:       rel.agora,
	})

	ctx, cancel := context.WithCancel(context.Background())
	fim := make(chan struct{})
	go func() { s.Rodar(ctx); close(fim) }()
	defer func() {
		cancel()
		<-fim
	}()

	// depois do erro, nao redespacha durante o backoff.
	if !esperar(func() bool { return stub.execCount(1) == 1 }, 2*time.Second) {
		t.Fatal("demanda nao rodou a primeira vez")
	}
	time.Sleep(30 * time.Millisecond)
	if stub.execCount(1) != 1 {
		t.Fatalf("demanda redespachada durante o backoff (execN=%d)", stub.execCount(1))
	}
	// passado o backoff, deve retentar e concluir.
	rel.avancar(6 * time.Minute)
	if !esperar(func() bool { return stub.execCount(1) == 2 }, 2*time.Second) {
		t.Fatalf("demanda nao foi retentada apos o backoff (execN=%d)", stub.execCount(1))
	}
}

func TestConcluidaNaoRedespacha(t *testing.T) {
	stub := novoStub() // default: tudo Concluido de primeira
	is := itens(3, 1)
	s := Novo(Opcoes{
		Fonte:     &fonteFixa{itens: is}, // fonte SEMPRE devolve as 3
		Executor:  stub,
		MaxGlobal: 3,
		Intervalo: 2 * time.Millisecond,
	})
	rodarPor(t, s, func() bool {
		for _, it := range is {
			if stub.execCount(it.DemandaID) == 0 {
				return false
			}
		}
		return true
	}, 2*time.Second)
	// deixa varios ticks passarem: como a fonte ainda devolve as 3, so o guard de
	// concluidas impede o reprocessamento.
	time.Sleep(40 * time.Millisecond)
	for _, it := range is {
		if n := stub.execCount(it.DemandaID); n != 1 {
			t.Fatalf("demanda %d rodou %d vezes; concluida nao deveria redespachar", it.DemandaID, n)
		}
	}
}

func TestFonteBancoLeStatusAgendaveis(t *testing.T) {
	d := abrirTempDB(t)
	ctx := context.Background()
	proj := projetoTeste(t, d)

	criarDem := func(status string, prio int) int64 {
		dem, err := d.CriarDemanda(ctx, db.Demanda{ProjectID: proj, Titulo: status, Status: status, Prioridade: prio})
		if err != nil {
			t.Fatalf("criar demanda (%s): %v", status, err)
		}
		return dem.ID
	}
	idPronta := criarDem(db.StatusDemandaPronta, 1)
	idExec := criarDem(db.StatusDemandaExecutando, 2)
	idFranquia := criarDem(db.StatusDemandaAguardandoFranquia, 3)
	criarDem(db.StatusDemandaConcluida, 4)           // nao agendavel
	criarDem(db.StatusDemandaAguardandoAprovacao, 5) // nao agendavel

	f := NovaFonteBanco(d)
	itens, err := f.Prontas(ctx)
	if err != nil {
		t.Fatalf("Prontas: %v", err)
	}
	got := map[int64]bool{}
	for _, it := range itens {
		if got[it.DemandaID] {
			t.Fatalf("demanda %d duplicada na fila", it.DemandaID)
		}
		got[it.DemandaID] = true
	}
	quero := []int64{idPronta, idExec, idFranquia}
	if len(itens) != len(quero) {
		t.Fatalf("fila com %d itens, esperava %d (%v)", len(itens), len(quero), itens)
	}
	for _, id := range quero {
		if !got[id] {
			t.Fatalf("demanda %d (agendavel) ausente da fila", id)
		}
	}
}

func TestMarcarStatusFranquiaNoBanco(t *testing.T) {
	d := abrirTempDB(t)
	ctx := context.Background()
	proj := projetoTeste(t, d)
	dem, err := d.CriarDemanda(ctx, db.Demanda{ProjectID: proj, Titulo: "x", Status: db.StatusDemandaPronta})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}

	rel := novoRelogio()
	stub := novoStub()
	primeira := make(chan struct{})
	var uma sync.Once
	stub.desfecho = func(item Item, n int) (Desfecho, error) {
		uma.Do(func() { close(primeira) })
		// franquia perpetua: fica sempre aguardando (nao conclui) para o teste
		// observar o status no banco.
		return Desfecho{RetomarEm: rel.agora().Add(time.Hour)}, nil
	}
	s := Novo(Opcoes{
		Fonte:     NovaFonteBanco(d),
		Executor:  stub,
		MaxGlobal: 1,
		Intervalo: 2 * time.Millisecond,
		Store:     d,
		Agora:     rel.agora,
	})
	ctx2, cancel := context.WithCancel(ctx)
	fim := make(chan struct{})
	go func() { s.Rodar(ctx2); close(fim) }()
	defer func() { cancel(); <-fim }()

	select {
	case <-primeira:
	case <-time.After(2 * time.Second):
		t.Fatal("executor nao rodou")
	}
	// apos a franquia, a demanda deve ficar aguardando_franquia no banco.
	if !esperar(func() bool {
		atual, err := d.ObterDemanda(ctx, dem.ID)
		return err == nil && atual.Status == db.StatusDemandaAguardandoFranquia
	}, 2*time.Second) {
		atual, _ := d.ObterDemanda(ctx, dem.ID)
		t.Fatalf("status da demanda = %q, esperava %q", atual.Status, db.StatusDemandaAguardandoFranquia)
	}
}

// --- helpers ------------------------------------------------------------------

func esperar(cond func() bool, timeout time.Duration) bool {
	prazo := time.Now().Add(timeout)
	for time.Now().Before(prazo) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

func abrirTempDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Abrir(filepath.Join(t.TempDir(), "praxis.db"))
	if err != nil {
		t.Fatalf("abrir db: %v", err)
	}
	t.Cleanup(func() { _ = d.Fechar() })
	return d
}

func projetoTeste(t *testing.T, d *db.DB) int64 {
	t.Helper()
	p, err := d.CriarProjeto(context.Background(), db.Projeto{
		Nome: "Proj", Slug: "proj", Pasta: `C:\repo`,
		BranchPrincipal: "main", ModoIntegracao: "merge_request", Ativo: true,
	})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	return p.ID
}
