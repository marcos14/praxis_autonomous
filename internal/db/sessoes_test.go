package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

// prazosTeste são as validades usadas na maioria dos testes: 1h de inatividade,
// teto de 24h.
var prazosTeste = PrazosSessao{Inatividade: time.Hour, Maxima: 24 * time.Hour}

// novoUsuarioParaSessao cria um usuário (sem papéis) e devolve o id.
func novoUsuarioParaSessao(t *testing.T, d *DB, email string) int64 {
	t.Helper()
	u, err := d.CriarUsuario(context.Background(), "Usuária de Sessão", email, "senha-forte-123", nil)
	if err != nil {
		t.Fatalf("CriarUsuario: %v", err)
	}
	return u.ID
}

// iso formata um instante no formato dos timestamps do banco.
func iso(t time.Time) string { return t.UTC().Format(formatoISO) }

// ajustarSessao regrava colunas de tempo da sessão direto no banco (simula a
// passagem do tempo sem mexer no relógio).
func ajustarSessao(t *testing.T, d *DB, id int64, ultimoUso, expiraEm, limiteEm string) {
	t.Helper()
	if _, err := d.Escritor.Exec(
		`UPDATE sessoes SET ultimo_uso = ?, expira_em = ?, limite_em = ? WHERE id = ?`,
		ultimoUso, expiraEm, limiteEm, id); err != nil {
		t.Fatalf("ajustar sessão %d: %v", id, err)
	}
}

func TestCriarAutenticarSessao(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	uid := novoUsuarioParaSessao(t, d, "a@x.test")

	s, err := d.CriarSessao(ctx, uid, "Mozilla/5.0 (teste)", "10.0.0.1", prazosTeste)
	if err != nil {
		t.Fatalf("CriarSessao: %v", err)
	}
	if s.ID == 0 || s.Token == "" || s.UserID != uid {
		t.Fatalf("sessão sem id/token/usuário: %+v", s)
	}
	if !(s.ExpiraEm < s.LimiteEm) {
		t.Fatalf("expira_em %q deveria vir antes de limite_em %q", s.ExpiraEm, s.LimiteEm)
	}
	if s.UserAgent != "Mozilla/5.0 (teste)" || s.IP != "10.0.0.1" {
		t.Fatalf("user_agent/ip = %q/%q", s.UserAgent, s.IP)
	}

	// só o hash é persistido.
	var hash string
	if err := d.Leitor.QueryRow(`SELECT token_hash FROM sessoes WHERE id = ?`, s.ID).Scan(&hash); err != nil {
		t.Fatalf("ler hash: %v", err)
	}
	if hash == s.Token || hash != HashToken(s.Token) {
		t.Fatalf("token_hash = %q, quero o SHA-256 do token (e nunca o valor em claro)", hash)
	}

	got, err := d.AutenticarSessao(ctx, s.Token, time.Hour)
	if err != nil {
		t.Fatalf("AutenticarSessao: %v", err)
	}
	if got.ID != s.ID || got.UserID != uid {
		t.Fatalf("autenticada = %+v, quero id %d do usuário %d", got, s.ID, uid)
	}
	if got.Token != "" {
		t.Fatalf("autenticação não deve devolver o token em claro: %q", got.Token)
	}

	for _, tok := range []string{"nao-existe", "", "   "} {
		if _, err := d.AutenticarSessao(ctx, tok, time.Hour); !errors.Is(err, ErrSessaoInvalida) {
			t.Fatalf("token %q: err = %v, quero ErrSessaoInvalida", tok, err)
		}
	}
}

func TestCriarSessaoValidacoes(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	if _, err := d.CriarSessao(ctx, 999, "", "", prazosTeste); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("usuário inexistente: err = %v, quero ErrNaoEncontrado", err)
	}
	if _, err := d.CriarSessao(ctx, 0, "", "", prazosTeste); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("usuário 0: err = %v, quero ErrNaoEncontrado", err)
	}
	uid := novoUsuarioParaSessao(t, d, "v@x.test")
	if _, err := d.CriarSessao(ctx, uid, "", "", PrazosSessao{}); err == nil {
		t.Fatal("prazos zerados deveriam falhar")
	}
	if _, err := d.CriarSessao(ctx, uid, "", "", PrazosSessao{Inatividade: time.Hour}); err == nil {
		t.Fatal("teto zerado deveria falhar")
	}
}

func TestCriarSessaoTruncaUserAgent(t *testing.T) {
	d := abrirTemp(t)
	uid := novoUsuarioParaSessao(t, d, "ua@x.test")
	longo := make([]rune, 0, 400)
	for i := 0; i < 400; i++ {
		longo = append(longo, 'ç')
	}
	s, err := d.CriarSessao(context.Background(), uid, string(longo), "", prazosTeste)
	if err != nil {
		t.Fatalf("CriarSessao: %v", err)
	}
	if n := len([]rune(s.UserAgent)); n != maxUserAgent {
		t.Fatalf("user_agent com %d runas, quero %d", n, maxUserAgent)
	}
}

func TestAutenticarSessaoDeslizaExpiracao(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	uid := novoUsuarioParaSessao(t, d, "d@x.test")
	s, err := d.CriarSessao(ctx, uid, "", "", prazosTeste)
	if err != nil {
		t.Fatalf("CriarSessao: %v", err)
	}

	// uso logo em seguida: dentro do intervalo mínimo, nada é regravado.
	got, err := d.AutenticarSessao(ctx, s.Token, time.Hour)
	if err != nil {
		t.Fatalf("AutenticarSessao (1): %v", err)
	}
	if got.UltimoUso != s.UltimoUso || got.ExpiraEm != s.ExpiraEm {
		t.Fatalf("uso recente regravou: %q/%q → %q/%q", s.UltimoUso, s.ExpiraEm, got.UltimoUso, got.ExpiraEm)
	}

	// simula 10 min sem uso: ultimo_uso no passado, expira_em ainda no futuro.
	agora := time.Now().UTC()
	ajustarSessao(t, d, s.ID, iso(agora.Add(-10*time.Minute)), iso(agora.Add(50*time.Minute)), s.LimiteEm)
	got, err = d.AutenticarSessao(ctx, s.Token, time.Hour)
	if err != nil {
		t.Fatalf("AutenticarSessao (2): %v", err)
	}
	if !(got.ExpiraEm > iso(agora.Add(55*time.Minute))) {
		t.Fatalf("expira_em não deslizou: %q (agora %s)", got.ExpiraEm, iso(agora))
	}
	if !(got.UltimoUso >= iso(agora.Add(-time.Second))) {
		t.Fatalf("ultimo_uso não avançou: %q", got.UltimoUso)
	}
	// o deslize foi persistido.
	var expiraDB, usoDB string
	if err := d.Leitor.QueryRow(`SELECT expira_em, ultimo_uso FROM sessoes WHERE id = ?`, s.ID).Scan(&expiraDB, &usoDB); err != nil {
		t.Fatalf("ler sessão: %v", err)
	}
	if expiraDB != got.ExpiraEm || usoDB != got.UltimoUso {
		t.Fatalf("banco = %q/%q, memória = %q/%q", expiraDB, usoDB, got.ExpiraEm, got.UltimoUso)
	}
}

func TestAutenticarSessaoRespeitaTeto(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	uid := novoUsuarioParaSessao(t, d, "t@x.test")
	// inatividade maior que o teto: expira_em nasce igual ao teto…
	s, err := d.CriarSessao(ctx, uid, "", "", PrazosSessao{Inatividade: time.Hour, Maxima: 30 * time.Minute})
	if err != nil {
		t.Fatalf("CriarSessao: %v", err)
	}
	if s.ExpiraEm != s.LimiteEm {
		t.Fatalf("expira_em %q deveria ser o teto %q", s.ExpiraEm, s.LimiteEm)
	}
	// …e o deslize nunca passa dele.
	agora := time.Now().UTC()
	ajustarSessao(t, d, s.ID, iso(agora.Add(-10*time.Minute)), s.ExpiraEm, s.LimiteEm)
	got, err := d.AutenticarSessao(ctx, s.Token, time.Hour)
	if err != nil {
		t.Fatalf("AutenticarSessao: %v", err)
	}
	if got.ExpiraEm != s.LimiteEm {
		t.Fatalf("deslize passou do teto: expira_em %q, teto %q", got.ExpiraEm, s.LimiteEm)
	}
}

func TestAutenticarSessaoExpirada(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	uid := novoUsuarioParaSessao(t, d, "e@x.test")
	agora := time.Now().UTC()

	// inatividade vencida (teto ainda no futuro).
	s1, _ := d.CriarSessao(ctx, uid, "", "", prazosTeste)
	ajustarSessao(t, d, s1.ID, iso(agora.Add(-2*time.Hour)), iso(agora.Add(-time.Minute)), s1.LimiteEm)
	if _, err := d.AutenticarSessao(ctx, s1.Token, time.Hour); !errors.Is(err, ErrSessaoInvalida) {
		t.Fatalf("inatividade vencida: err = %v, quero ErrSessaoInvalida", err)
	}

	// teto vencido (expira_em ainda no futuro — não deve valer).
	s2, _ := d.CriarSessao(ctx, uid, "", "", prazosTeste)
	ajustarSessao(t, d, s2.ID, s2.UltimoUso, iso(agora.Add(time.Hour)), iso(agora.Add(-time.Minute)))
	if _, err := d.AutenticarSessao(ctx, s2.Token, time.Hour); !errors.Is(err, ErrSessaoInvalida) {
		t.Fatalf("teto vencido: err = %v, quero ErrSessaoInvalida", err)
	}

	// timestamp ilegível conta como vencido (fail-closed).
	s3, _ := d.CriarSessao(ctx, uid, "", "", prazosTeste)
	ajustarSessao(t, d, s3.ID, s3.UltimoUso, "lixo", s3.LimiteEm)
	if _, err := d.AutenticarSessao(ctx, s3.Token, time.Hour); !errors.Is(err, ErrSessaoInvalida) {
		t.Fatalf("timestamp ilegível: err = %v, quero ErrSessaoInvalida", err)
	}
}

func TestRevogarSessao(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	a := novoUsuarioParaSessao(t, d, "ra@x.test")
	b := novoUsuarioParaSessao(t, d, "rb@x.test")
	s, err := d.CriarSessao(ctx, a, "", "", prazosTeste)
	if err != nil {
		t.Fatalf("CriarSessao: %v", err)
	}

	// outro usuário não revoga a sessão de a.
	if err := d.RevogarSessao(ctx, s.ID, b); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("revogar sessão alheia: err = %v, quero ErrNaoEncontrado", err)
	}
	if _, err := d.AutenticarSessao(ctx, s.Token, time.Hour); err != nil {
		t.Fatalf("sessão deveria seguir ativa: %v", err)
	}

	// o dono revoga; a sessão para de autenticar.
	if err := d.RevogarSessao(ctx, s.ID, a); err != nil {
		t.Fatalf("revogar: %v", err)
	}
	if _, err := d.AutenticarSessao(ctx, s.Token, time.Hour); !errors.Is(err, ErrSessaoInvalida) {
		t.Fatalf("sessão revogada ainda autentica: err = %v", err)
	}
	// revogar de novo é no-op; inexistente → ErrNaoEncontrado.
	if err := d.RevogarSessao(ctx, s.ID, a); err != nil {
		t.Fatalf("revogar 2x: %v", err)
	}
	if err := d.RevogarSessao(ctx, 9999, 0); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("revogar inexistente: err = %v, quero ErrNaoEncontrado", err)
	}

	// userID 0 (uso interno) revoga sem restrição de dono.
	s2, _ := d.CriarSessao(ctx, b, "", "", prazosTeste)
	if err := d.RevogarSessao(ctx, s2.ID, 0); err != nil {
		t.Fatalf("revogar sem dono: %v", err)
	}
	if _, err := d.AutenticarSessao(ctx, s2.Token, time.Hour); !errors.Is(err, ErrSessaoInvalida) {
		t.Fatalf("sessão revogada (sem dono) ainda autentica: err = %v", err)
	}
}

func TestRevogarSessaoPorToken(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	uid := novoUsuarioParaSessao(t, d, "pt@x.test")
	s, _ := d.CriarSessao(ctx, uid, "", "", prazosTeste)

	if err := d.RevogarSessaoPorToken(ctx, s.Token); err != nil {
		t.Fatalf("revogar por token: %v", err)
	}
	if _, err := d.AutenticarSessao(ctx, s.Token, time.Hour); !errors.Is(err, ErrSessaoInvalida) {
		t.Fatalf("sessão revogada ainda autentica: err = %v", err)
	}
	if err := d.RevogarSessaoPorToken(ctx, "desconhecido"); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("token desconhecido: err = %v, quero ErrNaoEncontrado", err)
	}
	if err := d.RevogarSessaoPorToken(ctx, ""); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("token vazio: err = %v, quero ErrNaoEncontrado", err)
	}
}

func TestRevogarSessoesDoUsuarioExceto(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	a := novoUsuarioParaSessao(t, d, "xa@x.test")
	b := novoUsuarioParaSessao(t, d, "xb@x.test")
	s1, _ := d.CriarSessao(ctx, a, "", "", prazosTeste)
	s2, _ := d.CriarSessao(ctx, a, "", "", prazosTeste)
	s3, _ := d.CriarSessao(ctx, a, "", "", prazosTeste)
	sb, _ := d.CriarSessao(ctx, b, "", "", prazosTeste)

	n, err := d.RevogarSessoesDoUsuario(ctx, a, s2.ID)
	if err != nil {
		t.Fatalf("RevogarSessoesDoUsuario: %v", err)
	}
	if n != 2 {
		t.Fatalf("revogadas = %d, quero 2", n)
	}
	if _, err := d.AutenticarSessao(ctx, s2.Token, time.Hour); err != nil {
		t.Fatalf("a sessão mantida deveria seguir ativa: %v", err)
	}
	for _, s := range []Sessao{s1, s3} {
		if _, err := d.AutenticarSessao(ctx, s.Token, time.Hour); !errors.Is(err, ErrSessaoInvalida) {
			t.Fatalf("sessão %d deveria estar revogada: err = %v", s.ID, err)
		}
	}
	if _, err := d.AutenticarSessao(ctx, sb.Token, time.Hour); err != nil {
		t.Fatalf("sessão de outro usuário foi afetada: %v", err)
	}

	// sem exceção: derruba todas as restantes; repetir não conta de novo.
	if n, _ := d.RevogarSessoesDoUsuario(ctx, a, 0); n != 1 {
		t.Fatalf("revogadas (todas) = %d, quero 1", n)
	}
	if n, _ := d.RevogarSessoesDoUsuario(ctx, a, 0); n != 0 {
		t.Fatalf("revogadas (repetição) = %d, quero 0", n)
	}
}

func TestListarSessoesDoUsuario(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	uid := novoUsuarioParaSessao(t, d, "l@x.test")
	outro := novoUsuarioParaSessao(t, d, "lo@x.test")
	agora := time.Now().UTC()

	vazio, err := d.ListarSessoesDoUsuario(ctx, uid)
	if err != nil || vazio == nil || len(vazio) != 0 {
		t.Fatalf("sem sessões: %v / %v, quero slice vazio não-nil", vazio, err)
	}

	antiga, _ := d.CriarSessao(ctx, uid, "navegador antigo", "", prazosTeste)
	recente, _ := d.CriarSessao(ctx, uid, "navegador recente", "", prazosTeste)
	revogada, _ := d.CriarSessao(ctx, uid, "revogada", "", prazosTeste)
	expirada, _ := d.CriarSessao(ctx, uid, "expirada", "", prazosTeste)
	_, _ = d.CriarSessao(ctx, outro, "de outro usuário", "", prazosTeste)

	ajustarSessao(t, d, antiga.ID, iso(agora.Add(-time.Hour)), iso(agora.Add(time.Hour)), antiga.LimiteEm)
	ajustarSessao(t, d, expirada.ID, iso(agora.Add(-3*time.Hour)), iso(agora.Add(-time.Minute)), expirada.LimiteEm)
	if err := d.RevogarSessao(ctx, revogada.ID, uid); err != nil {
		t.Fatalf("revogar: %v", err)
	}

	lista, err := d.ListarSessoesDoUsuario(ctx, uid)
	if err != nil {
		t.Fatalf("ListarSessoesDoUsuario: %v", err)
	}
	if len(lista) != 2 {
		t.Fatalf("len = %d, quero 2 (só as ativas): %+v", len(lista), lista)
	}
	if lista[0].ID != recente.ID || lista[1].ID != antiga.ID {
		t.Fatalf("ordem = [%d %d], quero [%d %d] (uso mais recente primeiro)",
			lista[0].ID, lista[1].ID, recente.ID, antiga.ID)
	}
	for _, s := range lista {
		if s.Token != "" {
			t.Fatalf("listagem não deve trazer token: %q", s.Token)
		}
		if s.UserID != uid {
			t.Fatalf("sessão de outro usuário na lista: %+v", s)
		}
	}
}

func TestRemoverSessoesExpiradas(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	uid := novoUsuarioParaSessao(t, d, "rm@x.test")
	agora := time.Now().UTC()

	ativa, _ := d.CriarSessao(ctx, uid, "", "", prazosTeste)
	expirada, _ := d.CriarSessao(ctx, uid, "", "", prazosTeste)
	tetoVencido, _ := d.CriarSessao(ctx, uid, "", "", prazosTeste)
	revogada, _ := d.CriarSessao(ctx, uid, "", "", prazosTeste)
	ajustarSessao(t, d, expirada.ID, expirada.UltimoUso, iso(agora.Add(-time.Minute)), expirada.LimiteEm)
	ajustarSessao(t, d, tetoVencido.ID, tetoVencido.UltimoUso, iso(agora.Add(time.Hour)), iso(agora.Add(-time.Minute)))
	if err := d.RevogarSessao(ctx, revogada.ID, uid); err != nil {
		t.Fatalf("revogar: %v", err)
	}

	n, err := d.RemoverSessoesExpiradas(ctx, agora)
	if err != nil {
		t.Fatalf("RemoverSessoesExpiradas: %v", err)
	}
	if n != 3 {
		t.Fatalf("removidas = %d, quero 3", n)
	}
	var restantes int
	var idRestante int64
	if err := d.Leitor.QueryRow(`SELECT COUNT(*), MAX(id) FROM sessoes`).Scan(&restantes, &idRestante); err != nil {
		t.Fatalf("contar: %v", err)
	}
	if restantes != 1 || idRestante != ativa.ID {
		t.Fatalf("restou %d sessão(ões), id %d; quero só a ativa %d", restantes, idRestante, ativa.ID)
	}
	if n, _ := d.RemoverSessoesExpiradas(ctx, agora); n != 0 {
		t.Fatalf("segunda passada removeu %d, quero 0", n)
	}
}
