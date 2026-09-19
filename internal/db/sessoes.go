package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// formatoISO é o formato dos timestamps do banco (ISO-8601 UTC com
// milissegundos) — o mesmo produzido pelos DEFAULTs strftime das tabelas, o que
// permite comparar instantes como texto no SQL.
const formatoISO = "2006-01-02T15:04:05.000Z"

// ErrSessaoInvalida é devolvido por AutenticarSessao quando não há sessão ativa
// para o token: inexistente, revogada ou expirada (por inatividade ou pelo teto
// absoluto). É único de propósito — o cliente não fica sabendo qual dos motivos.
var ErrSessaoInvalida = errors.New("sessão inválida ou expirada")

// intervaloRegravarUso é o mínimo entre duas regravações de ultimo_uso/expira_em
// pela mesma sessão. Cada renovação de token passa por AutenticarSessao;
// regravar a cada chamada seria uma escrita por usuário a cada renovação sem
// ganho — o deslize não precisa de precisão maior que isto.
const intervaloRegravarUso = 5 * time.Minute

// maxUserAgent limita o user_agent persistido (é só diagnóstico na lista de
// sessões; navegadores mandam strings longas).
const maxUserAgent = 200

// Sessao é uma linha de sessoes: a sessão persistida de um login (M1 do
// PLANO_INTERNET). O navegador guarda um token opaco num cookie HttpOnly e o
// banco guarda só o hash. ExpiraEm desliza a cada uso (inatividade); LimiteEm é
// o teto absoluto desde a criação e não desliza. Token só é preenchido na
// criação (valor em claro, entregue uma única vez ao cookie) e nunca é
// serializado.
type Sessao struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	CriadoEm   string `json:"criado_em"`
	UltimoUso  string `json:"ultimo_uso"`
	ExpiraEm   string `json:"expira_em"`
	LimiteEm   string `json:"limite_em"`
	UserAgent  string `json:"user_agent"`
	IP         string `json:"ip"`
	RevogadaEm string `json:"revogada_em,omitempty"`
	// Atual marca, numa listagem, a sessão da própria requisição. É preenchido
	// pela camada da API (o store não sabe qual cookie fez a chamada).
	Atual bool   `json:"atual"`
	Token string `json:"-"`
}

// PrazosSessao são as validades de uma sessão: Inatividade desliza a cada uso;
// Maxima é o teto absoluto a partir da criação. Ambos precisam ser positivos.
type PrazosSessao struct {
	Inatividade time.Duration
	Maxima      time.Duration
}

func (p PrazosSessao) validar() error {
	if p.Inatividade <= 0 || p.Maxima <= 0 {
		return errors.New("prazos da sessão precisam ser positivos")
	}
	return nil
}

// colunasSessao lista as colunas de sessoes na ordem esperada por scanSessao.
const colunasSessao = `id, user_id, criado_em, ultimo_uso, expira_em, limite_em,
	user_agent, ip, COALESCE(revogada_em, '')`

func scanSessao(sc interface{ Scan(...any) error }) (Sessao, error) {
	var s Sessao
	if err := sc.Scan(&s.ID, &s.UserID, &s.CriadoEm, &s.UltimoUso, &s.ExpiraEm, &s.LimiteEm,
		&s.UserAgent, &s.IP, &s.RevogadaEm); err != nil {
		return Sessao{}, err
	}
	return s, nil
}

// CriarSessao abre uma sessão para o usuário e devolve a linha com o token em
// CLARO no campo Token — a única vez que ele existe fora do cookie. Usuário
// inexistente vira ErrNaoEncontrado (violação de FK).
func (d *DB) CriarSessao(ctx context.Context, userID int64, userAgent, ip string, prazos PrazosSessao) (Sessao, error) {
	if userID <= 0 {
		return Sessao{}, ErrNaoEncontrado
	}
	if err := prazos.validar(); err != nil {
		return Sessao{}, err
	}
	plano, err := GerarTokenAPI()
	if err != nil {
		return Sessao{}, err
	}
	agora := time.Now().UTC()
	limite := agora.Add(prazos.Maxima)
	expira := deslizarAte(agora.Add(prazos.Inatividade), limite)
	s := Sessao{
		UserID:    userID,
		CriadoEm:  agora.Format(formatoISO),
		UltimoUso: agora.Format(formatoISO),
		ExpiraEm:  expira.Format(formatoISO),
		LimiteEm:  limite.Format(formatoISO),
		UserAgent: truncarRunas(strings.TrimSpace(userAgent), maxUserAgent),
		IP:        strings.TrimSpace(ip),
		Token:     plano,
	}
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO sessoes (user_id, token_hash, criado_em, ultimo_uso, expira_em, limite_em, user_agent, ip)
		VALUES (?,?,?,?,?,?,?,?)
		RETURNING id`,
		s.UserID, HashToken(plano), s.CriadoEm, s.UltimoUso, s.ExpiraEm, s.LimiteEm, s.UserAgent, s.IP)
	if err := row.Scan(&s.ID); err != nil {
		return Sessao{}, traduzirErroFK(err)
	}
	return s, nil
}

// AutenticarSessao resolve um token em claro para sua sessão ATIVA (não
// revogada, dentro da inatividade e do teto) e desliza a expiração por mais
// `inatividade` a partir de agora — sem passar do teto. Para não transformar
// cada renovação numa escrita, o deslize só é gravado quando o último uso tem
// mais de intervaloRegravarUso. Qualquer problema vira ErrSessaoInvalida.
func (d *DB) AutenticarSessao(ctx context.Context, token string, inatividade time.Duration) (Sessao, error) {
	token = strings.TrimSpace(token)
	if token == "" || inatividade <= 0 {
		return Sessao{}, ErrSessaoInvalida
	}
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasSessao+` FROM sessoes WHERE token_hash = ?`, HashToken(token))
	s, err := scanSessao(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Sessao{}, ErrSessaoInvalida
	}
	if err != nil {
		return Sessao{}, fmt.Errorf("autenticar sessão: %w", err)
	}
	agora := time.Now().UTC()
	if !sessaoAtiva(s, agora) {
		return Sessao{}, ErrSessaoInvalida
	}
	// Uso recente: devolve sem regravar (o deslize anterior ainda vale).
	if ultimo, err := time.Parse(formatoISO, s.UltimoUso); err == nil && agora.Sub(ultimo) < intervaloRegravarUso {
		return s, nil
	}
	limite, err := time.Parse(formatoISO, s.LimiteEm)
	if err != nil {
		return Sessao{}, fmt.Errorf("autenticar sessão %d: limite ilegível %q", s.ID, s.LimiteEm)
	}
	s.UltimoUso = agora.Format(formatoISO)
	s.ExpiraEm = deslizarAte(agora.Add(inatividade), limite).Format(formatoISO)
	if _, err := d.Escritor.ExecContext(ctx,
		`UPDATE sessoes SET ultimo_uso = ?, expira_em = ? WHERE id = ? AND revogada_em IS NULL`,
		s.UltimoUso, s.ExpiraEm, s.ID); err != nil {
		return Sessao{}, fmt.Errorf("deslizar sessão %d: %w", s.ID, err)
	}
	return s, nil
}

// RevogarSessao revoga a sessão id. userID > 0 restringe à sessão daquele
// usuário (a rota "encerrar sessão" do próprio usuário); 0 não restringe (uso
// interno/administrativo). Inexistente — ou de outro usuário — vira
// ErrNaoEncontrado; já revogada é no-op.
func (d *DB) RevogarSessao(ctx context.Context, id, userID int64) error {
	cond, args := "id = ?", []any{id}
	if userID > 0 {
		cond += " AND user_id = ?"
		args = append(args, userID)
	}
	res, err := d.Escritor.ExecContext(ctx,
		`UPDATE sessoes SET revogada_em = ? WHERE `+cond+` AND revogada_em IS NULL`,
		append([]any{agoraISO()}, args...)...)
	if err != nil {
		return fmt.Errorf("revogar sessão %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revogar sessão %d: %w", id, err)
	}
	if n == 0 {
		// ou não existe (para este usuário), ou já estava revogada.
		var existe int
		if err := d.Leitor.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sessoes WHERE `+cond, args...).Scan(&existe); err != nil {
			return fmt.Errorf("revogar sessão %d: %w", id, err)
		}
		if existe == 0 {
			return ErrNaoEncontrado
		}
	}
	return nil
}

// RevogarSessaoPorToken revoga a sessão do token em claro (o logout pelo
// cookie). Token desconhecido vira ErrNaoEncontrado — o chamador decide se
// ignora (para o cliente, logout é idempotente).
func (d *DB) RevogarSessaoPorToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return ErrNaoEncontrado
	}
	var id int64
	err := d.Leitor.QueryRowContext(ctx,
		`SELECT id FROM sessoes WHERE token_hash = ?`, HashToken(token)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNaoEncontrado
	}
	if err != nil {
		return fmt.Errorf("revogar sessão por token: %w", err)
	}
	return d.RevogarSessao(ctx, id, 0)
}

// RevogarSessoesDoUsuario revoga todas as sessões ativas do usuário, exceto a
// de id `exceto` (0 = nenhuma exceção). Devolve quantas foram revogadas. É o
// que a troca de senha ("encerrar as outras") e a desativação do usuário usam.
func (d *DB) RevogarSessoesDoUsuario(ctx context.Context, userID, exceto int64) (int64, error) {
	res, err := d.Escritor.ExecContext(ctx,
		`UPDATE sessoes SET revogada_em = ? WHERE user_id = ? AND id <> ? AND revogada_em IS NULL`,
		agoraISO(), userID, exceto)
	if err != nil {
		return 0, fmt.Errorf("revogar sessões do usuário %d: %w", userID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revogar sessões do usuário %d: %w", userID, err)
	}
	return n, nil
}

// ListarSessoesDoUsuario devolve as sessões ATIVAS do usuário (não revogadas e
// não expiradas), da mais recentemente usada para a mais antiga — a lista da
// tela "Minha conta". Slice não-nil; Token nunca é preenchido.
func (d *DB) ListarSessoesDoUsuario(ctx context.Context, userID int64) ([]Sessao, error) {
	agora := agoraISO()
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT `+colunasSessao+` FROM sessoes
		WHERE user_id = ? AND revogada_em IS NULL AND expira_em > ? AND limite_em > ?
		ORDER BY ultimo_uso DESC, id DESC`, userID, agora, agora)
	if err != nil {
		return nil, fmt.Errorf("listar sessões do usuário %d: %w", userID, err)
	}
	defer rows.Close()
	sessoes := []Sessao{}
	for rows.Next() {
		s, err := scanSessao(rows)
		if err != nil {
			return nil, err
		}
		sessoes = append(sessoes, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar sessões do usuário %d: %w", userID, err)
	}
	return sessoes, nil
}

// RemoverSessoesExpiradas apaga as sessões que já não podem autenticar em
// `agora`: revogadas ou com expira_em/limite_em vencidos. É a rotina de
// retenção (manutenção). Devolve quantas linhas saíram.
func (d *DB) RemoverSessoesExpiradas(ctx context.Context, agora time.Time) (int64, error) {
	ts := agora.UTC().Format(formatoISO)
	res, err := d.Escritor.ExecContext(ctx,
		`DELETE FROM sessoes WHERE revogada_em IS NOT NULL OR expira_em <= ? OR limite_em <= ?`, ts, ts)
	if err != nil {
		return 0, fmt.Errorf("remover sessões expiradas: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("remover sessões expiradas: %w", err)
	}
	return n, nil
}

// sessaoAtiva informa se a sessão está ativa em `agora`: não revogada e antes de
// expira_em e de limite_em. Timestamps ilegíveis contam como vencidos
// (fail-closed).
func sessaoAtiva(s Sessao, agora time.Time) bool {
	if s.RevogadaEm != "" {
		return false
	}
	for _, ts := range []string{s.ExpiraEm, s.LimiteEm} {
		t, err := time.Parse(formatoISO, ts)
		if err != nil || !agora.Before(t) {
			return false
		}
	}
	return true
}

// deslizarAte devolve o menor entre o instante desejado e o teto.
func deslizarAte(desejado, teto time.Time) time.Time {
	if desejado.After(teto) {
		return teto
	}
	return desejado
}

// agoraISO devolve o instante atual no formato dos timestamps do banco.
func agoraISO() string { return time.Now().UTC().Format(formatoISO) }

// truncarRunas corta s em no máximo n runas (sem partir um caractere).
func truncarRunas(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
