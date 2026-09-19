package main

import (
	"os"
	"strings"
)

// envBool interpreta uma variável de ambiente como booleano: 1, true, yes, sim
// e on (sem distinguir maiúsculas) ligam; qualquer outro valor — ou ausência —
// desliga. Serve de default para flags que também podem vir do ambiente (útil
// para o serviço do sistema, onde editar a linha de comando é mais chato que
// definir uma variável).
func envBool(nome string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(nome))) {
	case "1", "true", "yes", "sim", "on":
		return true
	}
	return false
}
