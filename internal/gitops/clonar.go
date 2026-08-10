package gitops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Clonar clona url em destino (que NÃO pode existir), com variáveis extras no
// ambiente (a chave SSH do usuário — Fase C). É a primitiva do cadastro de
// projeto por clone: o serviço de clones cuida do job assíncrono e da criação
// do projeto; aqui só o git. O ctx cancela o clone (shutdown do serviço).
//
// Não passa pelo AmbienteRede do Ops: antes do projeto existir não há repo a
// resolver — o chamador já sabe QUAL chave usar e a entrega pronta em env.
func Clonar(ctx context.Context, url, destino string, env []string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return fmt.Errorf("gitops: url de clone vazia")
	}
	if _, err := os.Stat(destino); err == nil {
		return fmt.Errorf("gitops: o destino %s já existe", destino)
	}
	if err := os.MkdirAll(filepath.Dir(destino), 0o755); err != nil {
		return fmt.Errorf("gitops: criar a pasta dos clones: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--", url, destino)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	saida, err := cmd.CombinedOutput()
	if err != nil {
		// Clone interrompido deixa lixo: remove para o retry partir do zero.
		_ = os.RemoveAll(destino)
		return fmt.Errorf("git clone %s: %v — %s", url, err, ultimaLinha(string(saida)))
	}
	return nil
}

// ultimaLinha devolve a última linha não-vazia de s (o git põe o erro útil no
// fim do stderr).
func ultimaLinha(s string) string {
	linhas := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(linhas) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(linhas[i]); l != "" {
			return l
		}
	}
	return ""
}
