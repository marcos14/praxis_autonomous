package api

// Download do certificado TLS público do servidor (GET /cert). Existe para o
// operador instalar o certificado autoassinado como CONFIÁVEL nos dispositivos
// que acessam o Praxis pela rede: só aceitar o aviso do navegador não basta —
// o Chrome aplica a exceção à página, mas recusa o certificado nas conexões
// WebSocket, e o IDE web depende delas. A rota é pública de propósito: o
// certificado é o lado público do par (o navegador o recebe em todo handshake).

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// registrarRotasCert registra o download do certificado autoassinado. Quando o
// serve roda sem -tls (ou com certificado próprio do operador), o arquivo não
// existe em PRAXIS_HOME/tls e a rota devolve 404.
func (s *Servidor) registrarRotasCert(mux *http.ServeMux) {
	mux.HandleFunc("GET /cert", s.handleBaixarCert)
}

func (s *Servidor) handleBaixarCert(w http.ResponseWriter, r *http.Request) {
	home, err := db.PraxisHome()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pem, err := os.ReadFile(filepath.Join(home, "tls", "cert.pem"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="praxis.crt"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pem)
}
