package gitops

import "strings"

// Identidade do Praxis nos commits: o Praxis roda num servidor multiusuário,
// então a identidade de um commit NUNCA vem da config git da máquina — ela é
// injetada por comando, via variáveis GIT_AUTHOR_*/GIT_COMMITTER_* (ver env).
//
//   - AUTOR: o usuário da plataforma que criou a demanda (nome + e-mail do
//     cadastro; o e-mail é o login, obrigatório e único). Com Sufixo ligado
//     (config git_sufixo_praxis, default ligado), o nome ganha " - Praxis"
//     para marcar visualmente que o commit saiu da plataforma.
//   - COMMITTER: sempre o Praxis — quem APLICA o commit é a plataforma, no
//     modelo nativo do git para "fulano escreveu, o sistema commitou".
//
// Sem usuário (demanda antiga, token de API, modo bootstrap), autor e
// committer caem na identidade do próprio Praxis.
const (
	// PraxisNome é o nome do committer de todo commit do orquestrador (e o
	// autor, quando a demanda não tem usuário criador).
	PraxisNome = "Praxis"
	// PraxisEmail é o e-mail da identidade do Praxis. Domínio reservado
	// (.invalid) de propósito: não entrega e não colide com contas reais das
	// plataformas git.
	PraxisEmail = "praxis@praxis.invalid"
	// SufixoAutor é o sufixo opcional do nome do autor (config
	// git_sufixo_praxis, default ligado).
	SufixoAutor = " - Praxis"
)

// Identidade descreve o autor de um commit feito pelo orquestrador. O valor
// zero é válido e cai na identidade do próprio Praxis.
type Identidade struct {
	Nome   string // nome do usuário da plataforma
	Email  string // e-mail do usuário (login, obrigatório no cadastro)
	Sufixo bool   // acrescenta " - Praxis" ao nome do autor
}

// IdentidadePraxis é a identidade de fallback: o próprio Praxis como autor
// (demanda sem usuário criador, token de API, bootstrap).
func IdentidadePraxis() Identidade {
	return Identidade{Nome: PraxisNome, Email: PraxisEmail}
}

// AutorNome devolve o nome do autor já saneado, com o sufixo " - Praxis"
// quando configurado. Nome vazio (ou só caracteres inválidos) cai em
// PraxisNome — nunca devolve vazio, o git rejeitaria o commit.
func (i Identidade) AutorNome() string {
	nome := sanitizarIdent(i.Nome)
	if nome == "" {
		return PraxisNome
	}
	if i.Sufixo && nome != PraxisNome {
		nome += SufixoAutor
	}
	return nome
}

// AutorEmail devolve o e-mail do autor já saneado; vazio cai em PraxisEmail.
func (i Identidade) AutorEmail() string {
	email := sanitizarIdent(i.Email)
	if email == "" {
		return PraxisEmail
	}
	return email
}

// env devolve as variáveis de ambiente que fixam autor e committer de um
// comando git que cria commit (commit, merge). Passar por env — e não por
// `git config` — é o que isola demandas concorrentes de usuários diferentes:
// worktrees compartilham o .git/config do repo, mas cada comando tem seu
// próprio ambiente.
func (i Identidade) env() []string {
	return []string{
		"GIT_AUTHOR_NAME=" + i.AutorNome(),
		"GIT_AUTHOR_EMAIL=" + i.AutorEmail(),
		"GIT_COMMITTER_NAME=" + PraxisNome,
		"GIT_COMMITTER_EMAIL=" + PraxisEmail,
	}
}

// sanitizarIdent remove o que corromperia (ou falsificaria) o ident de um
// commit: < e > (delimitadores do ident), quebras de linha e demais caracteres
// de controle, que permitiriam injetar trailers/cabeçalhos falsos. O resto do
// texto (acentos, espaços) passa intacto.
func sanitizarIdent(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '<' || r == '>' || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
