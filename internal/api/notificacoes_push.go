package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/notify"
	"github.com/marcos14/praxis-autonomous/internal/webpush"
)

// registrarRotasPush registra a assinatura Web Push por dispositivo e as
// preferências de notificação do usuário (M4.F4.E2). As preferências ficam
// sob /auth por serem da conta (como senha e idioma).
func (s *Servidor) registrarRotasPush(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/notificacoes/push/chave", s.handleChavePush)
	mux.HandleFunc("POST /api/v1/notificacoes/push", s.handleAssinarPush)
	mux.HandleFunc("DELETE /api/v1/notificacoes/push", s.handleCancelarPush)
	mux.HandleFunc("GET /api/v1/auth/preferencias", s.handleObterPreferencias)
	mux.HandleFunc("PUT /api/v1/auth/preferencias", s.handleDefinirPreferencias)
}

// handleChavePush devolve a chave pública VAPID da instância — o
// applicationServerKey que o navegador usa em pushManager.subscribe.
func (s *Servidor) handleChavePush(w http.ResponseWriter, r *http.Request) {
	if _, ok := usuarioComNotificacoes(w, r); !ok {
		return
	}
	pub, _, err := s.banco.ObterOuGerarVAPID(r.Context(), func() (string, string, error) {
		ch, err := webpush.GerarChaves()
		return ch.Publica, ch.Privada, err
	})
	if err != nil {
		s.log.Error("chave vapid", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	responderJSON(w, http.StatusOK, map[string]string{"chave": pub})
}

// reqAssinaturaPush é o PushSubscription.toJSON() do navegador.
type reqAssinaturaPush struct {
	Endpoint       string `json:"endpoint"`
	ExpirationTime any    `json:"expirationTime,omitempty"`
	Keys           struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// respAssinaturaPush é o que a API devolve da assinatura (sem as chaves).
type respAssinaturaPush struct {
	ID       int64  `json:"id"`
	Endpoint string `json:"endpoint"`
	CriadoEm string `json:"criado_em"`
}

// handleAssinarPush grava (ou renova, pelo endpoint) a assinatura deste
// dispositivo para o usuário logado.
func (s *Servidor) handleAssinarPush(w http.ResponseWriter, r *http.Request) {
	uid, ok := usuarioComNotificacoes(w, r)
	if !ok {
		return
	}
	var req reqAssinaturaPush
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if !strings.HasPrefix(strings.TrimSpace(req.Endpoint), "https://") {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.assinatura_push_invalida")
		return
	}
	a, err := s.banco.SalvarAssinaturaPush(r.Context(), db.AssinaturaPush{
		UserID: uid, Endpoint: req.Endpoint, P256dh: req.Keys.P256dh, Auth: req.Keys.Auth, UserAgent: r.UserAgent(),
	})
	if err != nil {
		if errors.Is(err, db.ErrAssinaturaInvalida) {
			erroT(w, r, http.StatusBadRequest, "invalido", "erro.assinatura_push_invalida")
			return
		}
		s.log.Error("salvar assinatura push", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	responderJSON(w, http.StatusCreated, respAssinaturaPush{ID: a.ID, Endpoint: a.Endpoint, CriadoEm: a.CriadoEm})
}

// handleCancelarPush apaga a assinatura deste dispositivo (pelo endpoint) —
// só as do próprio usuário. Idempotente: desconhecida → 204 também.
func (s *Servidor) handleCancelarPush(w http.ResponseWriter, r *http.Request) {
	uid, ok := usuarioComNotificacoes(w, r)
	if !ok {
		return
	}
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Endpoint) == "" {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.assinatura_push_invalida")
		return
	}
	if err := s.banco.RemoverAssinaturaPush(r.Context(), req.Endpoint, uid); err != nil && !errors.Is(err, db.ErrNaoEncontrado) {
		s.log.Error("remover assinatura push", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// respPreferencias é o GET das preferências: os valores efetivos (padrão do
// catálogo por baixo do que foi salvo), quantos dispositivos deste usuário
// têm push assinado e a lista dos tipos ligados por padrão (para a UI marcar
// "restaurar padrão").
type respPreferencias struct {
	notify.Preferencias
	Assinaturas int      `json:"assinaturas"`
	PadraoTipos []string `json:"padrao_tipos"`
}

// handleObterPreferencias devolve as preferências efetivas do usuário.
func (s *Servidor) handleObterPreferencias(w http.ResponseWriter, r *http.Request) {
	uid, ok := usuarioComNotificacoes(w, r)
	if !ok {
		return
	}
	raw, err := s.banco.PreferenciasNotificacao(r.Context(), uid)
	if err != nil {
		s.log.Error("ler preferências", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	assinaturas, err := s.banco.ListarAssinaturasDoUsuario(r.Context(), uid)
	if err != nil {
		s.log.Error("listar assinaturas push", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	responderJSON(w, http.StatusOK, respPreferencias{
		Preferencias: notify.DecodificarPreferencias(raw),
		Assinaturas:  len(assinaturas),
		PadraoTipos:  notify.TiposPadraoUsuario,
	})
}

// handleDefinirPreferencias grava as preferências. `eventos` deve trazer os
// tipos com valor explícito (tipos ausentes mantêm o padrão do catálogo ao
// serem lidos); tipo fora do catálogo → 400.
func (s *Servidor) handleDefinirPreferencias(w http.ResponseWriter, r *http.Request) {
	uid, ok := usuarioComNotificacoes(w, r)
	if !ok {
		return
	}
	var req notify.Preferencias
	if !decodificarCorpo(w, r, &req) {
		return
	}
	for tipo := range req.Eventos {
		if !notify.TipoConhecido(tipo) {
			erroT(w, r, http.StatusBadRequest, "invalido", "erro.tipo_evento_desconhecido", "tipo", tipo)
			return
		}
	}
	if req.Eventos == nil {
		req.Eventos = map[string]bool{}
	}
	raw, err := json.Marshal(req)
	if err != nil {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.corpo_invalido", "detalhe", err.Error())
		return
	}
	if err := s.banco.DefinirPreferenciasNotificacao(r.Context(), uid, string(raw)); err != nil {
		s.log.Error("definir preferências", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
