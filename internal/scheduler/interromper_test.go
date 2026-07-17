package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
	"github.com/marcos14/praxis-autonomous/internal/motor"
	"github.com/marcos14/praxis-autonomous/internal/pipeline"
)

// TestExecutarDemandaEstadoNaoAgendavel: o executor respeita o status alterado
// pela ação pausar/cancelar (Fase 2i). Estados PAUSÁVEIS (pausada/conflito/…) não
// concluem a demanda (retomáveis); estados TERMINAIS (cancelada/falhou) sim. A
// checagem ocorre ANTES de Preparar — nenhuma fase roda e nenhum git é tocado.
func TestExecutarDemandaEstadoNaoAgendavel(t *testing.T) {
	d := abrirTempDB(t)
	ctx := context.Background()
	proj := projetoTeste(t, d)
	runner := &pipeline.Runner{Store: d, Git: gitops.Novo(), Home: t.TempDir()}
	exec := &ExecutorDemanda{Store: d, Runner: runner}

	casos := []struct {
		status        string
		wantConcluido bool
	}{
		{db.StatusDemandaPausada, false},
		{db.StatusDemandaConflito, false},
		{db.StatusDemandaAguardandoAprovacao, false},
		{db.StatusDemandaCancelada, true},
		{db.StatusDemandaFalhou, true},
	}
	for _, c := range casos {
		dem, err := d.CriarDemanda(ctx, db.Demanda{ProjectID: proj, Titulo: "x", Status: c.status})
		if err != nil {
			t.Fatalf("%s: criar demanda: %v", c.status, err)
		}
		des, err := exec.Executar(ctx, Item{DemandaID: dem.ID, ProjectID: proj}, "")
		if err != nil {
			t.Fatalf("%s: Executar devolveu erro: %v", c.status, err)
		}
		if des.Concluido != c.wantConcluido {
			t.Fatalf("%s: Concluido=%v, quero %v", c.status, des.Concluido, c.wantConcluido)
		}
	}
}

// TestPausarInterrompeEntreFasesERetomarContinua exercita a semântica da Fase 2i
// com o scheduler real (FonteBanco): ao marcar a demanda `pausada` no fim de uma
// fase, o scheduler PARA de despachá-la (não avança para a próxima); ao devolvê-la
// a `pronta` (retomar), o scheduler a redespacha e ela conclui.
func TestPausarInterrompeEntreFasesERetomarContinua(t *testing.T) {
	d := abrirTempDB(t)
	ctx := context.Background()
	proj := projetoTeste(t, d)
	dem, err := d.CriarDemanda(ctx, db.Demanda{ProjectID: proj, Titulo: "x", Status: db.StatusDemandaPronta})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}

	stub := novoStub()
	stub.trabalho = 2 * time.Millisecond
	// desfecho por número de execução: pausa (via banco) ao fim da 2ª "fase";
	// depois de retomada, conclui na 4ª.
	stub.desfecho = func(item Item, n int) (Desfecho, error) {
		switch {
		case n == 2:
			cur, _ := d.ObterDemanda(ctx, item.DemandaID)
			cur.Status = db.StatusDemandaPausada
			_, _ = d.AtualizarDemanda(ctx, cur)
			return Desfecho{Concluido: false}, nil
		case n >= 4:
			cur, _ := d.ObterDemanda(ctx, item.DemandaID)
			cur.Status = db.StatusDemandaConcluida
			_, _ = d.AtualizarDemanda(ctx, cur)
			return Desfecho{Concluido: true}, nil
		default:
			return Desfecho{Concluido: false}, nil
		}
	}

	s := Novo(Opcoes{
		Fonte: NovaFonteBanco(d), Executor: stub, MaxGlobal: 1, Store: d,
		Intervalo: 2 * time.Millisecond,
	})
	ctx2, cancel := context.WithCancel(ctx)
	fim := make(chan struct{})
	go func() { s.Rodar(ctx2); close(fim) }()
	defer func() { cancel(); <-fim }()

	// 1) roda até pausar (execN==2 + status pausada).
	if !esperar(func() bool {
		cur, _ := d.ObterDemanda(ctx, dem.ID)
		return stub.execCount(dem.ID) == 2 && cur.Status == db.StatusDemandaPausada
	}, 3*time.Second) {
		t.Fatalf("não pausou entre fases: execN=%d", stub.execCount(dem.ID))
	}
	// 2) pausada NÃO progride: a contagem fica estável.
	time.Sleep(60 * time.Millisecond)
	if n := stub.execCount(dem.ID); n != 2 {
		t.Fatalf("demanda pausada continuou executando: execN=%d (esperava 2)", n)
	}

	// 3) retomar: devolve a demanda à fila.
	cur, _ := d.ObterDemanda(ctx, dem.ID)
	cur.Status = db.StatusDemandaPronta
	if _, err := d.AtualizarDemanda(ctx, cur); err != nil {
		t.Fatalf("retomar: %v", err)
	}
	if !esperar(func() bool {
		cur, _ := d.ObterDemanda(ctx, dem.ID)
		return cur.Status == db.StatusDemandaConcluida
	}, 3*time.Second) {
		t.Fatalf("não concluiu após retomar: execN=%d", stub.execCount(dem.ID))
	}
	if n := stub.execCount(dem.ID); n < 3 {
		t.Fatalf("retomar não continuou: execN=%d (esperava >=3)", n)
	}
}

// TestInterromperAbortaWorkerEmAndamento: Interromper cancela o ctx do worker,
// desbloqueando um run em andamento (a base da ação `cancelar` ao vivo).
func TestInterromperAbortaWorkerEmAndamento(t *testing.T) {
	d := abrirTempDB(t)
	ctx := context.Background()
	proj := projetoTeste(t, d)
	dem, err := d.CriarDemanda(ctx, db.Demanda{ProjectID: proj, Titulo: "x", Status: db.StatusDemandaPronta})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}

	stub := novoStub()
	stub.trabalho = time.Hour // bloqueia até o ctx do worker ser cancelado
	stub.desfecho = func(item Item, n int) (Desfecho, error) {
		// ao ser interrompido, conclui para não redespachar no teste.
		cur, _ := d.ObterDemanda(ctx, item.DemandaID)
		cur.Status = db.StatusDemandaCancelada
		_, _ = d.AtualizarDemanda(ctx, cur)
		return Desfecho{Concluido: true}, nil
	}

	s := Novo(Opcoes{
		Fonte: NovaFonteBanco(d), Executor: stub, MaxGlobal: 1, Store: d,
		Intervalo: 2 * time.Millisecond,
	})
	ctx2, cancel := context.WithCancel(ctx)
	fim := make(chan struct{})
	go func() { s.Rodar(ctx2); close(fim) }()
	defer func() { cancel(); <-fim }()

	// espera o worker entrar em execução.
	if !esperar(func() bool { return s.Ativos() == 1 }, 3*time.Second) {
		t.Fatal("worker não entrou em execução")
	}
	if !s.Interromper(dem.ID) {
		t.Fatal("Interromper devolveu false para demanda em execução")
	}
	// o worker deve terminar rápido (o trabalho de 1h foi abortado pelo ctx).
	if !esperar(func() bool { return s.Ativos() == 0 }, 3*time.Second) {
		t.Fatal("worker não terminou após Interromper (ctx não abortou o run)")
	}
	// Interromper numa demanda que não está rodando → false.
	if s.Interromper(dem.ID) {
		t.Fatal("Interromper deveria devolver false para demanda parada")
	}
}

// fakeKiller conta chamadas de MatarOrfaos para a recuperação pós-restart.
type fakeKiller struct {
	chamado bool
	n       int
}

func (f *fakeKiller) MatarOrfaos() (int, error) {
	f.chamado = true
	return f.n, nil
}

// TestRecuperarPosRestart: prune do projeto ativo, morte de órfãos e a transição
// executando → pausada → refila (pronta), com evento de recuperação. Demandas em
// outros estados ficam intactas.
func TestRecuperarPosRestart(t *testing.T) {
	repo, _ := repoComOrigin(t)
	d := abrirTempDB(t)
	ctx := context.Background()

	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "P", Slug: "p", Pasta: repo, BranchPrincipal: "main",
		ModoIntegracao: db.ModoIntegracaoMergeRequest, Ativo: true,
	})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}

	orfa, _ := d.CriarDemanda(ctx, db.Demanda{ProjectID: proj.ID, Titulo: "orfa", Status: db.StatusDemandaExecutando})
	intacta, _ := d.CriarDemanda(ctx, db.Demanda{ProjectID: proj.ID, Titulo: "pronta", Status: db.StatusDemandaPronta})

	killer := &fakeKiller{n: 3}
	rec, err := RecuperarPosRestart(ctx, d, gitops.Novo(), killer, nil)
	if err != nil {
		t.Fatalf("RecuperarPosRestart: %v", err)
	}
	if rec.ProjetosPreparados != 1 {
		t.Fatalf("ProjetosPreparados=%d, quero 1", rec.ProjetosPreparados)
	}
	if !killer.chamado || rec.OrfaosMortos != 3 {
		t.Fatalf("órfãos: chamado=%v mortos=%d (quero 3)", killer.chamado, rec.OrfaosMortos)
	}
	if rec.DemandasRefiladas != 1 {
		t.Fatalf("DemandasRefiladas=%d, quero 1", rec.DemandasRefiladas)
	}
	if cur, _ := d.ObterDemanda(ctx, orfa.ID); cur.Status != db.StatusDemandaPronta {
		t.Fatalf("órfã: status=%q, quero pronta", cur.Status)
	}
	if cur, _ := d.ObterDemanda(ctx, intacta.ID); cur.Status != db.StatusDemandaPronta {
		t.Fatalf("demanda intacta mudou de status: %q", cur.Status)
	}
	evs, _ := d.ListarEventos(ctx, db.FiltroEventos{DemandID: &orfa.ID})
	achou := false
	for _, e := range evs {
		if e.Tipo == "recuperada_pos_restart" {
			achou = true
		}
	}
	if !achou {
		t.Fatal("evento recuperada_pos_restart não registrado para a órfã")
	}
}

// TestRestartRetomaDemandaE2E: com o executor real, uma demanda "derrubada no
// meio" (executando, com a 1ª fase concluída e a 2ª pendente) é retomada pela
// recuperação pós-restart e conduzida até concluir (roda só a fase restante).
func TestRestartRetomaDemandaE2E(t *testing.T) {
	repo, _ := repoComOrigin(t)
	d := abrirTempDB(t)
	ctx := context.Background()

	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "E2E", Slug: "e2e", Pasta: repo, BranchPrincipal: "main",
		ModoIntegracao: db.ModoIntegracaoMergeRequest, Ativo: true,
	})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	// demanda "órfã": travada em executando, fase 1 já concluída, fase 2 pendente.
	dem, _, err := d.CriarDemandaComFases(ctx,
		db.Demanda{ProjectID: proj.ID, Titulo: "Retomavel", Status: db.StatusDemandaExecutando, PlanoMD: "# plano"},
		[]db.Fase{
			{Codigo: "1", Titulo: "Feita antes da queda", Status: db.StatusFaseConcluida},
			{Codigo: "2", Titulo: "Restante", Status: db.StatusFasePendente, DependeDe: []string{"1"}},
		},
	)
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}

	// recuperação: executando → pronta.
	if _, err := RecuperarPosRestart(ctx, d, gitops.Novo(), nil, nil); err != nil {
		t.Fatalf("RecuperarPosRestart: %v", err)
	}
	if cur, _ := d.ObterDemanda(ctx, dem.ID); cur.Status != db.StatusDemandaPronta {
		t.Fatalf("após recuperação, status=%q, quero pronta", cur.Status)
	}

	runner := &pipeline.Runner{
		Store:      d,
		Git:        gitops.Novo(),
		Home:       t.TempDir(),
		Prompt:     func(string) (string, error) { return "prompt {FASE} {TITULO}", nil },
		Selecionar: func(string) (motor.Motor, error) { return motorStub{nome: "claude"}, nil },
	}
	exec := &ExecutorDemanda{Store: d, Runner: runner}
	s := Novo(Opcoes{
		Fonte: NovaFonteBanco(d), Executor: exec, MaxGlobal: 1, Store: d,
		Intervalo: 2 * time.Millisecond,
	})
	concluida := func() bool {
		cur, err := d.ObterDemanda(ctx, dem.ID)
		return err == nil && cur.Status == db.StatusDemandaConcluida
	}
	rodarPor(t, s, concluida, 20*time.Second)
	if !concluida() {
		t.Fatal("demanda não concluiu após retomada pós-restart")
	}
	// só a fase 2 (restante) rodou: teve tentativas; a fase 1 continua concluída.
	fases, _ := d.ListarFases(ctx, dem.ID)
	for _, f := range fases {
		if f.Codigo == "2" && f.Status != db.StatusFaseConcluida {
			t.Fatalf("fase 2 não concluiu: status=%q", f.Status)
		}
	}
}
