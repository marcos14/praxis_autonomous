// Package chavessh gerencia as chaves SSH POR USUÁRIO do Praxis (Fase C do
// PLANO_MULTIUSUARIO.md): geração, leitura da pública e o GIT_SSH_COMMAND que
// faz clone/fetch/push usarem a chave certa.
//
// Cada usuário tem um par ed25519 em PRAXIS_HOME/ssh/u<id>/ (dir 0700, chave
// 0600), gerado pelo `ssh-keygen` do sistema — que acompanha o OpenSSH/git que
// o Praxis já exige; nenhuma dependência Go nova. A chave PRIVADA nunca sai do
// servidor, nunca entra no SQLite e nunca é devolvida pela API (mesma filosofia
// dos perfis de motor). O usuário copia a PÚBLICA pela UI e a cadastra no
// GitHub/GitLab/Gitea; a partir daí clona e opera repositórios pela web sem
// nenhum acesso ao SO do servidor.
//
// O known_hosts também vive em PRAXIS_HOME/ssh: o serviço roda com uma conta
// que pode nem ter HOME utilizável (LocalSystem), e `accept-new` registra o
// host na primeira conexão sem prompt — sem enfraquecer conexões seguintes.
package chavessh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ErrJaExiste indica que o usuário já tem chave (a geração não sobrescreve —
// perder a privada invalidaria o cadastro feito nas plataformas).
var ErrJaExiste = errors.New("o usuário já tem uma chave SSH")

// ErrNaoExiste indica que o usuário ainda não gerou a chave.
var ErrNaoExiste = errors.New("o usuário ainda não tem chave SSH")

// timeoutTeste limita o `git ls-remote` do botão "testar conexão".
const timeoutTeste = 30 * time.Second

// Chave é a parte PÚBLICA do par, com o fingerprint para conferência.
type Chave struct {
	Publica     string `json:"publica"`
	Fingerprint string `json:"fingerprint"`
}

// Gerente gerencia as chaves em uma raiz (PRAXIS_HOME/ssh).
type Gerente struct {
	// Dir é a raiz das chaves (PRAXIS_HOME/ssh).
	Dir string
}

// Novo cria o gerente com a raiz padrão sob o PRAXIS_HOME dado.
func Novo(praxisHome string) *Gerente {
	return &Gerente{Dir: filepath.Join(praxisHome, "ssh")}
}

// dirUsuario é a pasta da chave do usuário (u<id> — o id é estável; e-mails mudam).
func (g *Gerente) dirUsuario(userID int64) string {
	return filepath.Join(g.Dir, "u"+strconv.FormatInt(userID, 10))
}

// CaminhoPrivada devolve o caminho da chave privada do usuário.
func (g *Gerente) CaminhoPrivada(userID int64) string {
	return filepath.Join(g.dirUsuario(userID), "id_ed25519")
}

// Existe informa se o usuário já tem chave gerada.
func (g *Gerente) Existe(userID int64) bool {
	_, err := os.Stat(g.CaminhoPrivada(userID))
	return err == nil
}

// Gerar cria o par ed25519 do usuário via ssh-keygen (sem passphrase — a chave
// é usada por um serviço, não por uma pessoa num terminal) e devolve a pública.
// Usuário com chave existente devolve ErrJaExiste.
func (g *Gerente) Gerar(ctx context.Context, userID int64, comentario string) (Chave, error) {
	if g.Existe(userID) {
		return Chave{}, ErrJaExiste
	}
	dir := g.dirUsuario(userID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Chave{}, fmt.Errorf("criar %s: %w", dir, err)
	}
	comentario = strings.TrimSpace(comentario)
	if comentario == "" {
		comentario = "praxis-u" + strconv.FormatInt(userID, 10)
	}
	caminho := g.CaminhoPrivada(userID)
	cmd := exec.CommandContext(ctx, "ssh-keygen",
		"-t", "ed25519",
		"-N", "", // sem passphrase
		"-C", comentario,
		"-f", caminho,
	)
	if saida, err := cmd.CombinedOutput(); err != nil {
		return Chave{}, fmt.Errorf("ssh-keygen: %v — %s (o cliente OpenSSH está instalado?)",
			err, strings.TrimSpace(string(saida)))
	}
	// ssh-keygen já grava 0600/0644; reforça a privada por via das dúvidas
	// (em sistemas com umask exótico). Best-effort no Windows (ACLs mandam lá).
	_ = os.Chmod(caminho, 0o600)
	return g.Chave(userID)
}

// Chave devolve a pública + fingerprint do usuário (ErrNaoExiste sem chave).
func (g *Gerente) Chave(userID int64) (Chave, error) {
	pub, err := os.ReadFile(g.CaminhoPrivada(userID) + ".pub")
	if err != nil {
		if os.IsNotExist(err) {
			return Chave{}, ErrNaoExiste
		}
		return Chave{}, fmt.Errorf("ler a chave pública: %w", err)
	}
	c := Chave{Publica: strings.TrimSpace(string(pub))}
	// Fingerprint via ssh-keygen -l; falha vira campo vazio (é conferência, não
	// funcionalidade).
	if saida, err := exec.Command("ssh-keygen", "-l", "-f", g.CaminhoPrivada(userID)+".pub").Output(); err == nil {
		campos := strings.Fields(string(saida))
		if len(campos) >= 2 {
			c.Fingerprint = campos[1]
		}
	}
	return c, nil
}

// ComandoGit monta o valor de GIT_SSH_COMMAND para operar com a chave do
// usuário: identidade fixa (IdentitiesOnly ignora o agent e outras chaves),
// known_hosts do Praxis e accept-new (registra o host na 1ª conexão, sem
// prompt — conexões seguintes continuam verificadas).
func (g *Gerente) ComandoGit(userID int64) string {
	// Barras normais + aspas: o git interpreta GIT_SSH_COMMAND com sh, e os
	// caminhos do Windows têm "\" e possivelmente espaços.
	chave := filepath.ToSlash(g.CaminhoPrivada(userID))
	knownHosts := filepath.ToSlash(filepath.Join(g.Dir, "known_hosts"))
	return fmt.Sprintf(`ssh -i "%s" -o IdentitiesOnly=yes -o UserKnownHostsFile="%s" -o StrictHostKeyChecking=accept-new`,
		chave, knownHosts)
}

// AmbienteGit devolve o ambiente extra (GIT_SSH_COMMAND) para os comandos git
// que operam com a chave do usuário — nil quando o usuário não tem chave (as
// credenciais do SO seguem valendo, como sempre).
func (g *Gerente) AmbienteGit(userID int64) []string {
	if userID <= 0 || !g.Existe(userID) {
		return nil
	}
	return []string{"GIT_SSH_COMMAND=" + g.ComandoGit(userID)}
}

// Testar confirma que a chave do usuário alcança o repositório remoto: roda
// `git ls-remote <url> HEAD` com a chave. Devolve nil no sucesso e um erro com
// a última linha do stderr do git (legível: "Permission denied (publickey)",
// host inexistente etc.) na falha.
func (g *Gerente) Testar(ctx context.Context, userID int64, url string) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return errors.New("informe a URL do repositório (ex.: git@github.com:org/repo.git)")
	}
	if !g.Existe(userID) {
		return ErrNaoExiste
	}
	ctx, cancelar := context.WithTimeout(ctx, timeoutTeste)
	defer cancelar()
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--", url, "HEAD")
	cmd.Env = append(os.Environ(), g.AmbienteGit(userID)...)
	if saida, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s", ultimaLinhaNaoVazia(string(saida), err.Error()))
	}
	return nil
}

// ultimaLinhaNaoVazia devolve a última linha não-vazia de s, ou o fallback.
func ultimaLinhaNaoVazia(s, fallback string) string {
	linhas := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(linhas) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(linhas[i]); l != "" {
			return l
		}
	}
	return fallback
}
