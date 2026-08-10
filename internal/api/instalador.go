package api

// Endpoints do instalador de harnesses (Fase D do PLANO_MULTIUSUARIO.md): a
// tela Motores instala/atualiza o CLI oficial de um vendor direto pela web —
// download em job de background (como o clone de projetos), com progresso por
// poll. Tudo sob config.gerir: quem gerencia motores gerencia os CLIs deles.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// Estados de um job de instalação.
const (
	instalacaoStatusRodando   = "instalando"
	instalacaoStatusConcluido = "concluido"
	instalacaoStatusErro      = "erro"
)

// jobInstalacao é o estado de uma instalação de harness em andamento.
type jobInstalacao struct {
	ID       string                `json:"id"`
	Vendor   string                `json:"vendor"`
	Status   string                `json:"status"`
	Detalhe  string                `json:"detalhe,omitempty"`
	Info     *motor.InfoInstalacao `json:"info,omitempty"`
	CriadoEm time.Time             `json:"criado_em"`
}

// gerenteInstalacoes guarda os jobs em memória (um por vendor por vez). É
// instância do Servidor — não global — para servidores de teste não dividirem
// estado.
type gerenteInstalacoes struct {
	mu   sync.Mutex
	jobs map[string]*jobInstalacao
}

func novoGerenteInstalacoes() *gerenteInstalacoes {
	return &gerenteInstalacoes{jobs: map[string]*jobInstalacao{}}
}

// criar registra um job novo; recusa quando o MESMO vendor já está instalando.
func (g *gerenteInstalacoes) criar(vendor string) (*jobInstalacao, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	corte := time.Now().Add(-time.Hour)
	for id, v := range g.jobs {
		if v.Vendor == vendor && v.Status == instalacaoStatusRodando {
			return v, false
		}
		if v.Status != instalacaoStatusRodando && v.CriadoEm.Before(corte) {
			delete(g.jobs, id)
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, false
	}
	j := &jobInstalacao{ID: hex.EncodeToString(b), Vendor: vendor,
		Status: instalacaoStatusRodando, CriadoEm: time.Now()}
	g.jobs[j.ID] = j
	return j, true
}

func (g *gerenteInstalacoes) progresso(id, detalhe string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if j, ok := g.jobs[id]; ok && j.Status == instalacaoStatusRodando {
		j.Detalhe = detalhe
	}
}

func (g *gerenteInstalacoes) finalizar(id, status, detalhe string, info *motor.InfoInstalacao) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if j, ok := g.jobs[id]; ok {
		j.Status, j.Detalhe, j.Info = status, detalhe, info
	}
}

func (g *gerenteInstalacoes) obter(id string) (jobInstalacao, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	j, ok := g.jobs[id]
	if !ok {
		return jobInstalacao{}, false
	}
	return *j, true
}

// registrarRotasInstalador registra as rotas do instalador de harnesses.
func (s *Servidor) registrarRotasInstalador(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/engines/instalaveis", s.handleHarnessesInstalaveis)
	mux.HandleFunc("POST /api/v1/engines/instalar", s.handleInstalarHarness)
	mux.HandleFunc("GET /api/v1/engines/instalar/{jobId}", s.handleObterInstalacao)
}

// handleHarnessesInstalaveis devolve o estado do CLI de cada harness conhecido
// (instalado? de onde? qual versão?). É o que alimenta os botões Instalar/
// Atualizar da tela Motores.
func (s *Servidor) handleHarnessesInstalaveis(w http.ResponseWriter, r *http.Request) {
	responderJSON(w, http.StatusOK, motor.EstadoCLIs(r.Context()))
}

// reqInstalarHarness é o corpo de POST /engines/instalar.
type reqInstalarHarness struct {
	Vendor string `json:"vendor"`
}

// handleInstalarHarness dispara o download do CLI oficial do vendor em
// background e devolve 202 + job. Instalação já em andamento para o mesmo
// vendor devolve o job corrente (idempotente para a UI).
func (s *Servidor) handleInstalarHarness(w http.ResponseWriter, r *http.Request) {
	var req reqInstalarHarness
	if !decodificarCorpo(w, r, &req) {
		return
	}
	vendor := strings.ToLower(strings.TrimSpace(req.Vendor))
	if !motor.VendorInstalavel(vendor) {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.harness_nao_instalavel")
		return
	}
	job, novo := s.instalacoes.criar(vendor)
	if job == nil {
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	if novo {
		go func(id, vendor string) {
			// Background de propósito: o download sobrevive à requisição.
			info, err := motor.Instalar(context.Background(), vendor, func(msg string) {
				s.instalacoes.progresso(id, msg)
			})
			if err != nil {
				s.instalacoes.finalizar(id, instalacaoStatusErro, err.Error(), nil)
				return
			}
			s.instalacoes.finalizar(id, instalacaoStatusConcluido, "", &info)
		}(job.ID, vendor)
	}
	responderJSON(w, http.StatusAccepted, map[string]string{
		"job_id": job.ID,
		"status": instalacaoStatusRodando,
	})
}

// handleObterInstalacao devolve o estado de um job de instalação.
func (s *Servidor) handleObterInstalacao(w http.ResponseWriter, r *http.Request) {
	j, ok := s.instalacoes.obter(r.PathValue("jobId"))
	if !ok {
		erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.instalacao_nao_encontrada")
		return
	}
	responderJSON(w, http.StatusOK, j)
}
