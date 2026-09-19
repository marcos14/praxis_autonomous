package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// AssinaturaPush é uma linha de push_subscriptions: o endpoint e as chaves que
// o navegador entrega ao assinar Web Push num dispositivo (M4). Falhas conta
// envios recusados/inalcançáveis seguidos; ao passar do limite a assinatura é
// descartada (o dispositivo assina de novo quando abrir o Praxis).
type AssinaturaPush struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	Endpoint  string `json:"endpoint"`
	P256dh    string `json:"p256dh"`
	Auth      string `json:"auth"`
	UserAgent string `json:"user_agent"`
	CriadoEm  string `json:"criado_em"`
	UltimoUso string `json:"ultimo_uso,omitempty"`
	Falhas    int    `json:"falhas"`
}

// ErrAssinaturaInvalida indica endpoint/chaves em branco.
var ErrAssinaturaInvalida = errors.New("assinatura push inválida: endpoint e chaves são obrigatórios")

const colunasAssinatura = `id, user_id, endpoint, p256dh, auth, user_agent, criado_em, COALESCE(ultimo_uso, ''), falhas`

func scanAssinatura(sc interface{ Scan(...any) error }) (AssinaturaPush, error) {
	var a AssinaturaPush
	if err := sc.Scan(&a.ID, &a.UserID, &a.Endpoint, &a.P256dh, &a.Auth, &a.UserAgent,
		&a.CriadoEm, &a.UltimoUso, &a.Falhas); err != nil {
		return AssinaturaPush{}, err
	}
	return a, nil
}

// SalvarAssinaturaPush grava (ou atualiza, pelo endpoint) a assinatura de um
// dispositivo do usuário. Reassinar zera as falhas. Usuário inexistente vira
// ErrNaoEncontrado.
func (d *DB) SalvarAssinaturaPush(ctx context.Context, a AssinaturaPush) (AssinaturaPush, error) {
	a.Endpoint = strings.TrimSpace(a.Endpoint)
	a.P256dh = strings.TrimSpace(a.P256dh)
	a.Auth = strings.TrimSpace(a.Auth)
	if a.UserID <= 0 || a.Endpoint == "" || a.P256dh == "" || a.Auth == "" {
		return AssinaturaPush{}, ErrAssinaturaInvalida
	}
	a.UserAgent = truncarRunas(strings.TrimSpace(a.UserAgent), maxUserAgent)
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, user_agent)
		VALUES (?,?,?,?,?)
		ON CONFLICT(endpoint) DO UPDATE SET
			user_id = excluded.user_id, p256dh = excluded.p256dh, auth = excluded.auth,
			user_agent = excluded.user_agent, falhas = 0
		RETURNING `+colunasAssinatura,
		a.UserID, a.Endpoint, a.P256dh, a.Auth, a.UserAgent)
	salva, err := scanAssinatura(row)
	if err != nil {
		return AssinaturaPush{}, traduzirErroFK(err)
	}
	return salva, nil
}

// RemoverAssinaturaPush apaga a assinatura pelo endpoint. userID > 0 restringe
// às do usuário (a rota "desativar neste dispositivo"); 0 não restringe (o
// despachante ao receber 404/410). Inexistente vira ErrNaoEncontrado.
func (d *DB) RemoverAssinaturaPush(ctx context.Context, endpoint string, userID int64) error {
	cond, args := "endpoint = ?", []any{strings.TrimSpace(endpoint)}
	if userID > 0 {
		cond += " AND user_id = ?"
		args = append(args, userID)
	}
	res, err := d.Escritor.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE `+cond, args...)
	if err != nil {
		return fmt.Errorf("remover assinatura push: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remover assinatura push: %w", err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// ListarAssinaturasDoUsuario devolve as assinaturas push do usuário. Slice não-nil.
func (d *DB) ListarAssinaturasDoUsuario(ctx context.Context, userID int64) ([]AssinaturaPush, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasAssinatura+` FROM push_subscriptions WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("listar assinaturas push: %w", err)
	}
	defer rows.Close()
	lista := []AssinaturaPush{}
	for rows.Next() {
		a, err := scanAssinatura(rows)
		if err != nil {
			return nil, err
		}
		lista = append(lista, a)
	}
	return lista, rows.Err()
}

// RegistrarFalhaAssinatura incrementa as falhas da assinatura e a remove ao
// atingir maxFalhas (devolve removida=true). Inexistente vira ErrNaoEncontrado.
func (d *DB) RegistrarFalhaAssinatura(ctx context.Context, id int64, maxFalhas int) (bool, error) {
	var falhas int
	err := d.Escritor.QueryRowContext(ctx,
		`UPDATE push_subscriptions SET falhas = falhas + 1 WHERE id = ? RETURNING falhas`, id).Scan(&falhas)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNaoEncontrado
	}
	if err != nil {
		return false, fmt.Errorf("registrar falha da assinatura %d: %w", id, err)
	}
	if maxFalhas > 0 && falhas >= maxFalhas {
		if _, err := d.Escritor.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id = ?`, id); err != nil {
			return false, fmt.Errorf("descartar assinatura %d: %w", id, err)
		}
		return true, nil
	}
	return false, nil
}

// MarcarUsoAssinatura carimba ultimo_uso e zera as falhas após um envio aceito.
func (d *DB) MarcarUsoAssinatura(ctx context.Context, id int64) error {
	if _, err := d.Escritor.ExecContext(ctx,
		`UPDATE push_subscriptions SET ultimo_uso = ?, falhas = 0 WHERE id = ?`, agoraISO(), id); err != nil {
		return fmt.Errorf("marcar uso da assinatura %d: %w", id, err)
	}
	return nil
}

// RemoverAssinaturasSemUso apaga as assinaturas cujo último envio aceito (ou a
// criação, se nunca houve envio) é anterior a corte — retenção.
func (d *DB) RemoverAssinaturasSemUso(ctx context.Context, corte time.Time) (int64, error) {
	res, err := d.Escritor.ExecContext(ctx,
		`DELETE FROM push_subscriptions WHERE COALESCE(ultimo_uso, criado_em) < ?`, corte.UTC().Format(formatoISO))
	if err != nil {
		return 0, fmt.Errorf("remover assinaturas sem uso: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("remover assinaturas sem uso: %w", err)
	}
	return n, nil
}

// PreferenciasNotificacao devolve o JSON de preferências de notificação do
// usuário ("" = nunca definiu; o chamador aplica o padrão). Inexistente vira
// ErrNaoEncontrado.
func (d *DB) PreferenciasNotificacao(ctx context.Context, userID int64) (string, error) {
	var raw string
	err := d.Leitor.QueryRowContext(ctx, `SELECT notificacoes FROM users WHERE id = ?`, userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNaoEncontrado
	}
	if err != nil {
		return "", fmt.Errorf("preferências de notificação do usuário %d: %w", userID, err)
	}
	return raw, nil
}

// DefinirPreferenciasNotificacao grava o JSON de preferências do usuário (a
// validação do conteúdo é da API/despachante). Inexistente vira ErrNaoEncontrado.
func (d *DB) DefinirPreferenciasNotificacao(ctx context.Context, userID int64, raw string) error {
	res, err := d.Escritor.ExecContext(ctx, `UPDATE users SET notificacoes = ? WHERE id = ?`, raw, userID)
	if err != nil {
		return fmt.Errorf("definir preferências do usuário %d: %w", userID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("definir preferências do usuário %d: %w", userID, err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// ObterOuGerarVAPID devolve o par de chaves VAPID da instância (Web Push),
// gerando-o com `gerar` e persistindo em auth_config na primeira vez — mesmo
// padrão do segredo do JWT. Devolve (pública, privada), ambas no formato que
// `gerar` produziu (base64url).
func (d *DB) ObterOuGerarVAPID(ctx context.Context, gerar func() (publica, privada string, err error)) (string, string, error) {
	// Garante a linha 1 de auth_config (criada junto com o segredo do JWT).
	if _, err := d.ObterOuGerarJWTSecret(ctx); err != nil {
		return "", "", err
	}
	var pub, priv string
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT vapid_publica, vapid_privada FROM auth_config WHERE id = 1`).Scan(&pub, &priv); err != nil {
		return "", "", fmt.Errorf("ler chaves vapid: %w", err)
	}
	if pub != "" && priv != "" {
		return pub, priv, nil
	}
	pub, priv, err := gerar()
	if err != nil {
		return "", "", fmt.Errorf("gerar chaves vapid: %w", err)
	}
	// Só grava se ainda estiver vazio (corrida entre instâncias): depois relê.
	if _, err := d.Escritor.ExecContext(ctx,
		`UPDATE auth_config SET vapid_publica = ?, vapid_privada = ? WHERE id = 1 AND vapid_publica = ''`,
		pub, priv); err != nil {
		return "", "", fmt.Errorf("gravar chaves vapid: %w", err)
	}
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT vapid_publica, vapid_privada FROM auth_config WHERE id = 1`).Scan(&pub, &priv); err != nil {
		return "", "", fmt.Errorf("reler chaves vapid: %w", err)
	}
	return pub, priv, nil
}

// ChavePushContato é a config global com o contato do operador enviado ao
// serviço de push no VAPID (`sub`): um e-mail ou uma URL. Vazia → e-mail do
// primeiro administrador.
const ChavePushContato = "push_contato"

// ContatoPush resolve o `sub` do VAPID: a config `push_contato` ou, vazia, o
// e-mail do primeiro admin (EmailPrimeiroAdmin). E-mails ganham o prefixo
// `mailto:`; URLs (http/https) passam como estão. Sem nada → ErrNaoEncontrado.
func (d *DB) ContatoPush(ctx context.Context) (string, error) {
	entradas, err := d.ObterConfigGlobal(ctx)
	if err != nil {
		return "", err
	}
	var contato string
	if raw, ok := entradas[ChavePushContato]; ok {
		_ = json.Unmarshal(raw, &contato)
	}
	contato = strings.TrimSpace(contato)
	if contato == "" {
		if contato, err = d.EmailPrimeiroAdmin(ctx); err != nil {
			return "", err
		}
	}
	if strings.HasPrefix(contato, "mailto:") || strings.HasPrefix(contato, "http://") || strings.HasPrefix(contato, "https://") {
		return contato, nil
	}
	return "mailto:" + contato, nil
}
