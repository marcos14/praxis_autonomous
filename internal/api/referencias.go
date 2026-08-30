package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/referencias"
)

// Referências são os documentos de apoio que o usuário anexa a uma sessão de IA
// (planejamento ou consulta): ADRs de outros projetos, transcrições de reunião,
// planilhas, rascunhos. Ficam na subpasta `referencias/` da pasta de trabalho da
// sessão — o disco É a fonte da verdade (não há índice no banco) — e o harness
// as lê como INSUMO, sempre em modo leitura.
//
// Este arquivo concentra o mecanismo comum às duas features; cada uma expõe
// suas rotas (autorização, resolução do id e fala de sistema no chat) nos seus
// próprios handlers.

// respReferencia é uma referência anexada, como a API a devolve.
type respReferencia struct {
	Arquivo      string `json:"arquivo"`
	Tamanho      int64  `json:"tamanho"`
	ModificadoEm string `json:"modificado_em"`
}

// limiteReferencia é o tamanho máximo de um arquivo de referência (15 MiB —
// transcrições e PDFs cabem com folga; nada disso deveria ser um vídeo).
const limiteReferencia = 15 << 20

// listarReferenciasDir responde a listagem da pasta de referências. Pasta
// inexistente (nada anexado ainda) devolve lista vazia — não é erro.
func (s *Servidor) listarReferenciasDir(w http.ResponseWriter, dir string) {
	refs := []respReferencia{}
	for _, a := range referencias.Listar(dir) {
		refs = append(refs, respReferencia{Arquivo: a.Nome, Tamanho: a.Tamanho, ModificadoEm: a.ModTime})
	}
	responderJSON(w, http.StatusOK, refs)
}

// receberReferencia grava em dir o arquivo enviado em multipart/form-data
// (campo "arquivo"), validando nome, extensão e tamanho. Já responde o erro HTTP
// quando algo falha; devolve ok=false nesse caso.
func (s *Servidor) receberReferencia(w http.ResponseWriter, r *http.Request, dir string) (respReferencia, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, limiteReferencia+(1<<20))
	if err := r.ParseMultipartForm(limiteReferencia); err != nil {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.referencia_envio_invalido")
		return respReferencia{}, false
	}
	f, hdr, err := r.FormFile("arquivo")
	if err != nil {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.referencia_campo_arquivo_obrigatorio")
		return respReferencia{}, false
	}
	defer f.Close()

	nome := filepath.Base(strings.TrimSpace(hdr.Filename))
	if !referencias.NomeValido(nome) {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.referencia_nome_invalido")
		return respReferencia{}, false
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.log.Error("criar pasta de referências", "erro", err, "pasta", dir)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return respReferencia{}, false
	}
	destino := filepath.Join(dir, nome)
	dst, err := os.Create(destino)
	if err != nil {
		s.log.Error("gravar referência", "erro", err, "arquivo", destino)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return respReferencia{}, false
	}
	tamanho, err := io.Copy(dst, io.LimitReader(f, limiteReferencia+1))
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil || tamanho > limiteReferencia {
		_ = os.Remove(destino)
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.referencia_falha_receber")
		return respReferencia{}, false
	}
	return respReferencia{Arquivo: nome, Tamanho: tamanho}, true
}

// baixarReferencia devolve o arquivo SEMPRE como download (attachment):
// referência é insumo do usuário, nunca página a exibir no domínio do Praxis.
func (s *Servidor) baixarReferencia(w http.ResponseWriter, r *http.Request, dir, arquivo string) {
	if dir == "" || !referencias.NomeValido(arquivo) {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.referencia_invalida")
		return
	}
	conteudo, err := os.ReadFile(filepath.Join(dir, arquivo))
	if err != nil {
		erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.referencia_nao_encontrada")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", arquivo))
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(conteudo)
}

// excluirReferencia remove o arquivo da pasta. Já responde o erro HTTP quando
// algo falha; devolve ok=false nesse caso (o chamador responde 204 e registra a
// fala de sistema).
func (s *Servidor) excluirReferencia(w http.ResponseWriter, r *http.Request, dir, arquivo string) bool {
	if dir == "" || !referencias.NomeValido(arquivo) {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.referencia_invalida")
		return false
	}
	if err := os.Remove(filepath.Join(dir, arquivo)); err != nil {
		if os.IsNotExist(err) {
			erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.referencia_nao_encontrada")
			return false
		}
		s.log.Error("remover referência", "erro", err, "arquivo", filepath.Join(dir, arquivo))
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return false
	}
	return true
}
