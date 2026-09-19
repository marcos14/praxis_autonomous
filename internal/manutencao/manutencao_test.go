package manutencao

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

func abrirBanco(t *testing.T) *db.DB {
	t.Helper()
	caminho := filepath.Join(t.TempDir(), "praxis.db")
	d, err := db.Abrir(caminho)
	if err != nil {
		t.Fatalf("abrir banco: %v", err)
	}
	t.Cleanup(func() { _ = d.Fechar() })
	return d
}

func TestBackupGeraArquivoValido(t *testing.T) {
	d := abrirBanco(t)
	ctx := context.Background()
	// registra um evento para o backup ter conteúdo.
	if _, err := d.RegistrarEvento(ctx, db.Evento{Tipo: "x", Titulo: "t"}); err != nil {
		t.Fatalf("evento: %v", err)
	}

	dir := t.TempDir()
	m := Nova(Opcoes{Store: d, DirBackups: dir})
	caminho, err := m.Backup(ctx, time.Date(2026, 7, 17, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if _, err := os.Stat(caminho); err != nil {
		t.Fatalf("backup não criado: %v", err)
	}
	// o backup deve abrir como um banco válido com o mesmo schema.
	bkp, err := db.Abrir(caminho)
	if err != nil {
		t.Fatalf("abrir backup: %v", err)
	}
	defer bkp.Fechar()
	evs, err := bkp.ListarEventos(ctx, db.FiltroEventos{})
	if err != nil {
		t.Fatalf("ler backup: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("backup tem %d eventos, quero 1", len(evs))
	}
}

func TestRotacionarBackupsMantemMaisRecentes(t *testing.T) {
	dir := t.TempDir()
	nomes := []string{
		"praxis-20260101-000000.db",
		"praxis-20260102-000000.db",
		"praxis-20260103-000000.db",
		"praxis-20260104-000000.db",
	}
	for _, n := range nomes {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := RotacionarBackups(dir, 2); err != nil {
		t.Fatalf("rotacionar: %v", err)
	}
	restantes, _ := os.ReadDir(dir)
	if len(restantes) != 2 {
		t.Fatalf("restaram %d, quero 2", len(restantes))
	}
	// os dois mais recentes devem sobreviver.
	if _, err := os.Stat(filepath.Join(dir, "praxis-20260104-000000.db")); err != nil {
		t.Fatalf("mais recente foi removido: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "praxis-20260101-000000.db")); !os.IsNotExist(err) {
		t.Fatal("mais antigo deveria ter sido removido")
	}
}

func TestLimparLogsRemoveAntigos(t *testing.T) {
	dir := t.TempDir()
	antigo := filepath.Join(dir, "d1", "velho.jsonl")
	novo := filepath.Join(dir, "d1", "novo.jsonl")
	_ = os.MkdirAll(filepath.Dir(antigo), 0o755)
	if err := os.WriteFile(antigo, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(novo, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// envelhece o arquivo antigo.
	velho := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(antigo, velho, velho); err != nil {
		t.Fatal(err)
	}

	corte := time.Now().Add(-24 * time.Hour)
	n, err := LimparLogs(dir, corte)
	if err != nil {
		t.Fatalf("LimparLogs: %v", err)
	}
	if n != 1 {
		t.Fatalf("removidos = %d, quero 1", n)
	}
	if _, err := os.Stat(antigo); !os.IsNotExist(err) {
		t.Fatal("arquivo antigo deveria ter sido removido")
	}
	if _, err := os.Stat(novo); err != nil {
		t.Fatalf("arquivo novo não deveria ser removido: %v", err)
	}
}

func TestCicloRemoveEventosAntigos(t *testing.T) {
	d := abrirBanco(t)
	ctx := context.Background()
	// evento antigo: injeta com criado_em no passado direto no banco.
	if _, err := d.Escritor.ExecContext(ctx,
		`INSERT INTO events (tipo, titulo, detalhe, criado_em) VALUES ('x','antigo','','2020-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("inserir antigo: %v", err)
	}
	// evento recente.
	if _, err := d.RegistrarEvento(ctx, db.Evento{Tipo: "x", Titulo: "recente"}); err != nil {
		t.Fatalf("evento recente: %v", err)
	}

	m := Nova(Opcoes{Store: d, RetencaoDias: 30})
	m.Ciclo(ctx, time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC))

	evs, _ := d.ListarEventos(ctx, db.FiltroEventos{})
	if len(evs) != 1 || evs[0].Titulo != "recente" {
		t.Fatalf("após retenção: %+v, quero só o recente", evs)
	}
}

func TestCicloRemoveSessoesExpiradas(t *testing.T) {
	d := abrirBanco(t)
	ctx := context.Background()
	u, err := d.CriarUsuario(ctx, "U", "u@x.test", "senha-forte-123", nil)
	if err != nil {
		t.Fatalf("criar usuário: %v", err)
	}
	prazos := db.PrazosSessao{Inatividade: time.Hour, Maxima: 24 * time.Hour}
	ativa, err := d.CriarSessao(ctx, u.ID, "", "", prazos)
	if err != nil {
		t.Fatalf("sessão ativa: %v", err)
	}
	expirada, err := d.CriarSessao(ctx, u.ID, "", "", prazos)
	if err != nil {
		t.Fatalf("sessão expirada: %v", err)
	}
	if _, err := d.Escritor.ExecContext(ctx,
		`UPDATE sessoes SET expira_em = '2020-01-01T00:00:00.000Z' WHERE id = ?`, expirada.ID); err != nil {
		t.Fatalf("expirar sessão: %v", err)
	}

	m := Nova(Opcoes{Store: d})
	m.Ciclo(ctx, time.Now())

	var n int
	if err := d.Leitor.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessoes`).Scan(&n); err != nil {
		t.Fatalf("contar sessões: %v", err)
	}
	if n != 1 {
		t.Fatalf("sessões após o ciclo = %d, quero 1 (só a ativa)", n)
	}
	if _, err := d.AutenticarSessao(ctx, ativa.Token, time.Hour); err != nil {
		t.Fatalf("a sessão ativa deveria sobreviver ao ciclo: %v", err)
	}
}

func TestCicloRetencaoDeNotificacoesEAssinaturasPush(t *testing.T) {
	d := abrirBanco(t)
	ctx := context.Background()
	u, err := d.CriarUsuario(ctx, "U", "u@x.test", "senha-forte-123", nil)
	if err != nil {
		t.Fatalf("criar usuário: %v", err)
	}
	agora := time.Now()
	criar := func(titulo string, idade int, lida bool) int64 {
		n, err := d.CriarNotificacao(ctx, db.Notificacao{UserID: u.ID, Tipo: "aviso", Titulo: titulo})
		if err != nil {
			t.Fatalf("criar notificação: %v", err)
		}
		quando := agora.AddDate(0, 0, -idade).UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := d.Escritor.ExecContext(ctx, `UPDATE notificacoes SET criado_em = ? WHERE id = ?`, quando, n.ID); err != nil {
			t.Fatal(err)
		}
		if lida {
			if err := d.MarcarNotificacaoLida(ctx, n.ID, u.ID); err != nil {
				t.Fatal(err)
			}
		}
		return n.ID
	}
	criar("lida velha", 31, true)      // sai (lida > 30 d)
	criar("lida recente", 29, true)    // fica
	criar("não lida velha", 91, false) // sai (não lida > 90 d)
	criar("não lida média", 60, false) // fica (não lida ≤ 90 d, mesmo > 30)
	assinar := func(endpoint string, idade int) {
		if _, err := d.SalvarAssinaturaPush(ctx, db.AssinaturaPush{UserID: u.ID, Endpoint: endpoint, P256dh: "p", Auth: "a"}); err != nil {
			t.Fatal(err)
		}
		quando := agora.AddDate(0, 0, -idade).UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := d.Escritor.ExecContext(ctx, `UPDATE push_subscriptions SET criado_em = ? WHERE endpoint = ?`, quando, endpoint); err != nil {
			t.Fatal(err)
		}
	}
	assinar("https://push/velha", 181)  // sai (sem uso > 180 d)
	assinar("https://push/recente", 10) // fica

	m := Nova(Opcoes{Store: d})
	m.Ciclo(ctx, agora)

	restantes, err := d.ListarNotificacoes(ctx, u.ID, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	titulos := map[string]bool{}
	for _, n := range restantes {
		titulos[n.Titulo] = true
	}
	if len(restantes) != 2 || !titulos["lida recente"] || !titulos["não lida média"] {
		t.Fatalf("notificações após o ciclo: %+v", restantes)
	}
	ass, err := d.ListarAssinaturasDoUsuario(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ass) != 1 || ass[0].Endpoint != "https://push/recente" {
		t.Fatalf("assinaturas após o ciclo: %+v", ass)
	}
}
