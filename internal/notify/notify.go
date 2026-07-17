package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Canal é a configuração de um canal de notificação. Os campos usados dependem
// do tipo (chave no mapa Config.Canais):
//   - telegram: Token + ChatID
//   - discord/slack/google_chat: WebhookURL
//   - webhook (genérico): URL + Header opcional ("Nome: valor")
type Canal struct {
	Ativo      bool   `json:"ativo"`
	Token      string `json:"token,omitempty"`
	ChatID     string `json:"chat_id,omitempty"`
	WebhookURL string `json:"webhook_url,omitempty"`
	URL        string `json:"url,omitempty"`
	Header     string `json:"header,omitempty"`
}

// Config é a configuração de notificações (lida do banco — config_entries global,
// chave "notificacoes"). Canais mapeia o nome do canal → sua config; Eventos
// mapeia o tipo de evento → habilitado (ausência cai no default habilitado);
// Cabecalho é uma linha opcional de identificação no topo da mensagem.
type Config struct {
	Canais    map[string]Canal `json:"canais"`
	Eventos   map[string]bool  `json:"eventos"`
	Cabecalho string           `json:"cabecalho,omitempty"`
}

// Notificador envia as notificações (best-effort: uma falha nunca propaga).
// Portado de notificacoes.go do Praxis atual, com a config vinda do banco.
type Notificador struct {
	cli *http.Client
	// Aviso recebe mensagens de diagnóstico (falha de envio). Se nil, são
	// silenciosas. Injetável para os testes observarem.
	Aviso func(msg string)
}

// Novo devolve um Notificador com timeout padrão de HTTP.
func Novo() *Notificador {
	return &Notificador{cli: &http.Client{Timeout: 10 * time.Second}}
}

// AlgumCanalAtivo informa se ao menos um canal está ativo.
func AlgumCanalAtivo(cfg Config) bool {
	for _, c := range cfg.Canais {
		if c.Ativo {
			return true
		}
	}
	return false
}

// EventoLigado informa se o tipo de evento deve notificar. Sem entrada explícita
// em Eventos, cai no default (todos ligados) — para não silenciar eventos novos.
func EventoLigado(cfg Config, tipo string) bool {
	if cfg.Eventos == nil {
		return true
	}
	v, ok := cfg.Eventos[tipo]
	if !ok {
		return true
	}
	return v
}

// EnviarEvento envia a notificação de um evento se o tipo estiver ligado e houver
// canal ativo. É a entrada usada pelo despachante.
func (n *Notificador) EnviarEvento(ctx context.Context, cfg Config, tipo, titulo, corpo string) {
	if !EventoLigado(cfg, tipo) || !AlgumCanalAtivo(cfg) {
		return
	}
	n.Enviar(ctx, cfg, titulo, corpo)
}

// Enviar despacha titulo+corpo para todos os canais ativos (sem filtrar por
// evento). O cabeçalho da config, quando presente, vai na primeira linha.
func (n *Notificador) Enviar(ctx context.Context, cfg Config, titulo, corpo string) {
	partes := make([]string, 0, 3)
	if cab := strings.TrimSpace(cfg.Cabecalho); cab != "" {
		partes = append(partes, cab)
	}
	if t := strings.TrimSpace(titulo); t != "" {
		partes = append(partes, t)
	}
	if c := strings.TrimSpace(corpo); c != "" {
		partes = append(partes, c)
	}
	texto := strings.Join(partes, "\n")

	if c := cfg.Canais["telegram"]; c.Ativo {
		n.enviarTelegram(ctx, c, texto)
	}
	if c := cfg.Canais["discord"]; c.Ativo {
		n.postJSON(ctx, c.WebhookURL, map[string]string{"content": texto}, "")
	}
	if c := cfg.Canais["slack"]; c.Ativo {
		n.postJSON(ctx, c.WebhookURL, map[string]string{"text": texto}, "")
	}
	if c := cfg.Canais["google_chat"]; c.Ativo {
		n.postJSON(ctx, c.WebhookURL, map[string]string{"text": texto}, "")
	}
	if c := cfg.Canais["webhook"]; c.Ativo {
		n.postJSON(ctx, c.URL, map[string]string{"titulo": titulo, "texto": corpo}, c.Header)
	}
}

// enviarTelegram posta a mensagem via API do Telegram (sendMessage).
func (n *Notificador) enviarTelegram(ctx context.Context, c Canal, texto string) {
	token := strings.TrimSpace(c.Token)
	chat := strings.TrimSpace(c.ChatID)
	if token == "" || chat == "" {
		return
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	n.postJSON(ctx, url, map[string]string{"chat_id": chat, "text": texto}, "")
}

// postJSON faz um POST best-effort com corpo JSON e um cabeçalho opcional no
// formato "Nome: valor". Erros vão para Aviso (quando definido). Portado do
// Praxis atual.
func (n *Notificador) postJSON(ctx context.Context, url string, corpo map[string]string, header string) {
	if strings.TrimSpace(url) == "" {
		return
	}
	b, err := json.Marshal(corpo)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		n.avisar("notificação (%s): %v", encurtarURL(url), err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if h := strings.TrimSpace(header); h != "" {
		if i := strings.IndexByte(h, ':'); i > 0 {
			req.Header.Set(strings.TrimSpace(h[:i]), strings.TrimSpace(h[i+1:]))
		}
	}
	resp, err := n.cli.Do(req)
	if err != nil {
		n.avisar("notificação (%s): %v", encurtarURL(url), err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		n.avisar("notificação (%s) devolveu HTTP %d", encurtarURL(url), resp.StatusCode)
	}
}

func (n *Notificador) avisar(formato string, args ...any) {
	if n.Aviso != nil {
		n.Aviso(fmt.Sprintf(formato, args...))
	}
}

// encurtarURL esconde tokens/segredos ao logar uma URL (mostra só host+path).
func encurtarURL(url string) string {
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i] + "/…"
	}
	return s
}
