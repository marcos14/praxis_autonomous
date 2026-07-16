package db

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// nomeArquivoDB é o nome do arquivo do banco dentro do PRAXIS_HOME.
const nomeArquivoDB = "praxis.db"

// PraxisHome resolve o diretório base do Praxis Autonomous (onde vivem
// praxis.db, worktrees/ e logs/).
//
// Precedência:
//  1. variável de ambiente PRAXIS_HOME, se definida (permite override em testes
//     e em máquinas com layout customizado);
//  2. no Windows, %LOCALAPPDATA%\praxis (default do plano);
//  3. nos demais SOs, <os.UserConfigDir>/praxis.
//
// PraxisHome apenas calcula o caminho; não cria o diretório (ver CaminhoDB).
func PraxisHome() (string, error) {
	if h := strings.TrimSpace(os.Getenv("PRAXIS_HOME")); h != "" {
		return h, nil
	}
	if runtime.GOOS == "windows" {
		if la := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); la != "" {
			return filepath.Join(la, "praxis"), nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolver PRAXIS_HOME: %w", err)
	}
	return filepath.Join(base, "praxis"), nil
}

// CaminhoDB retorna o caminho do arquivo praxis.db dentro do PRAXIS_HOME,
// garantindo que o diretório base exista (cria-o se necessário).
func CaminhoDB() (string, error) {
	home, err := PraxisHome()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "", fmt.Errorf("criar PRAXIS_HOME %q: %w", home, err)
	}
	return filepath.Join(home, nomeArquivoDB), nil
}
