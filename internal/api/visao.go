package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// Chaves da config global que regem os itens SEM dono (criados por token de
// API ou no modo bootstrap) — M2 do PLANO_INTERNET, decisão da seção 2.
const (
	// ChaveSemDonoVisibilidade: admins (padrão) | grupo | publica.
	ChaveSemDonoVisibilidade = "sem_dono_visibilidade"
	// ChaveSemDonoGrupoID: id do grupo de usuários que vê os itens sem dono
	// quando o modo é grupo.
	ChaveSemDonoGrupoID = "sem_dono_grupo_id"
)

// ttlConfigSemDono é por quanto tempo a config de itens sem dono fica em cache
// (a leitura acontece em quase toda requisição de usuário comum). O PUT da
// config global invalida o cache, então mudar vale na hora mesmo assim.
const ttlConfigSemDono = 30 * time.Second

// configSemDono é a config resolvida dos itens sem dono.
type configSemDono struct {
	modo  string
	grupo int64
}

// cacheSemDono guarda a última leitura da config de itens sem dono.
type cacheSemDono struct {
	mu     sync.Mutex
	valor  configSemDono
	lidoEm time.Time
}

// configSemDono devolve a config de itens sem dono (cache curto). Config
// ausente, ilegível ou fora do catálogo cai em admins — o mais restritivo.
func (s *Servidor) configSemDono(ctx context.Context) configSemDono {
	padrao := configSemDono{modo: db.SemDonoAdmins}
	if s.banco == nil {
		return padrao
	}
	s.semDono.mu.Lock()
	defer s.semDono.mu.Unlock()
	if !s.semDono.lidoEm.IsZero() && time.Since(s.semDono.lidoEm) < ttlConfigSemDono {
		return s.semDono.valor
	}
	entradas, err := s.banco.ObterConfigGlobal(ctx)
	if err != nil {
		s.log.Warn("ler config de itens sem dono", "erro", err)
		return padrao
	}
	cfg := lerConfigSemDono(entradas)
	s.semDono.valor = cfg
	s.semDono.lidoEm = time.Now()
	return cfg
}

// invalidarConfigSemDono descarta o cache (chamado ao gravar a config global).
func (s *Servidor) invalidarConfigSemDono() {
	s.semDono.mu.Lock()
	defer s.semDono.mu.Unlock()
	s.semDono.lidoEm = time.Time{}
}

// lerConfigSemDono interpreta as chaves de itens sem dono de um mapa de config.
func lerConfigSemDono(entradas map[string]json.RawMessage) configSemDono {
	cfg := configSemDono{modo: db.SemDonoAdmins}
	if raw, ok := entradas[ChaveSemDonoVisibilidade]; ok {
		var modo string
		if err := json.Unmarshal(raw, &modo); err == nil && db.SemDonoValido(strings.TrimSpace(modo)) {
			cfg.modo = strings.TrimSpace(modo)
		}
	}
	cfg.grupo = int64(configInteiro(entradas, ChaveSemDonoGrupoID, 0))
	return cfg
}

// visaoDe monta a Visao do principal (M2): quem ele é (Usuario), se a ACL de
// projeto se aplica (mesma regra de filtroVisibilidade: não se aplica a
// projetos.gerir, tokens e bootstrap) e se a regra de dono se aplica — só
// admin `*`, tokens de API e bootstrap a ignoram. A config de itens sem dono
// entra apenas quando a regra de dono se aplica.
func (s *Servidor) visaoDe(ctx context.Context, pr *principal) db.Visao {
	v := db.Visao{ACL: filtroVisibilidade(pr)}
	if pr == nil || pr.userID <= 0 || pr.viaToken {
		return v
	}
	v.Usuario = pr.userID
	if pr.tem(db.PermCuringa) {
		return v
	}
	uid := pr.userID
	v.Dono = &uid
	cfg := s.configSemDono(ctx)
	v.SemDono = cfg.modo
	v.SemDonoGrupo = cfg.grupo
	return v
}

// visaoDaRequisicao é visaoDe para o principal da requisição.
func (s *Servidor) visaoDaRequisicao(r *http.Request) db.Visao {
	return s.visaoDe(r.Context(), principalDaRequisicao(r))
}

// visaoComEscopo é visaoDaRequisicao com o escopo de listagem vindo de
// ?escopo= (meus | grupo | todos; ausente = todos). Escopo desconhecido →
// responde 400 e devolve ok=false.
func (s *Servidor) visaoComEscopo(w http.ResponseWriter, r *http.Request) (db.Visao, bool) {
	escopo := strings.TrimSpace(r.URL.Query().Get("escopo"))
	if !db.EscopoValido(escopo) {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.escopo_invalido")
		return db.Visao{}, false
	}
	v := s.visaoDaRequisicao(r)
	v.Escopo = escopo
	return v, true
}

// visibilidadeDoCorpo lê e valida o campo `visibilidade` de um corpo de criação
// ("" = default do store, privada). Valor desconhecido → 400, ok=false.
func visibilidadeDoCorpo(w http.ResponseWriter, r *http.Request, v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v != "" && !db.VisibilidadeValida(v) {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.visibilidade_invalida")
		return "", false
	}
	return v, true
}

// reqVisibilidade é o corpo de PUT /{consultas|planejamentos|demands}/{id}/visibilidade.
type reqVisibilidade struct {
	Visibilidade string `json:"visibilidade"`
}

// podeAlterarVisibilidade informa se o principal pode mudar a visibilidade de um
// item cujo criador é criadoPor: o próprio criador ou um admin (`*`). Itens sem
// criador só o admin. Responde 403 e devolve false quando não pode.
func podeAlterarVisibilidade(w http.ResponseWriter, r *http.Request, criadoPor *int64) bool {
	pr := principalDaRequisicao(r)
	dono := criadoPor != nil && pr.userID > 0 && pr.userID == *criadoPor
	if dono || pr.tem(db.PermCuringa) {
		return true
	}
	erroT(w, r, http.StatusForbidden, "sem_permissao", "erro.visibilidade_sem_permissao")
	return false
}

// lerVisibilidadeDoPut decodifica e valida o corpo de um PUT de visibilidade.
func lerVisibilidadeDoPut(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req reqVisibilidade
	if !decodificarCorpo(w, r, &req) {
		return "", false
	}
	v := strings.TrimSpace(req.Visibilidade)
	if !db.VisibilidadeValida(v) {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.visibilidade_invalida")
		return "", false
	}
	return v, true
}

// validarConfigSemDono checa as chaves de itens sem dono num PUT da config
// global: modo no catálogo e, no modo grupo, um grupo de usuários existente.
// Responde 400 e devolve false quando inválido.
func (s *Servidor) validarConfigSemDono(w http.ResponseWriter, r *http.Request, entradas map[string]json.RawMessage) bool {
	modo := db.SemDonoAdmins
	if raw, ok := entradas[ChaveSemDonoVisibilidade]; ok {
		var m string
		if err := json.Unmarshal(raw, &m); err != nil || !db.SemDonoValido(strings.TrimSpace(m)) {
			erroT(w, r, http.StatusBadRequest, "invalido", "erro.config_sem_dono_invalida")
			return false
		}
		modo = strings.TrimSpace(m)
	}
	if modo == db.SemDonoGrupo {
		gid := int64(configInteiro(entradas, ChaveSemDonoGrupoID, 0))
		if gid <= 0 {
			erroT(w, r, http.StatusBadRequest, "invalido", "erro.config_sem_dono_grupo_inexistente")
			return false
		}
		if _, err := s.banco.ObterGrupoUsuarios(r.Context(), gid); err != nil {
			erroT(w, r, http.StatusBadRequest, "invalido", "erro.config_sem_dono_grupo_inexistente")
			return false
		}
	}
	return true
}
