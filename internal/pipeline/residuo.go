package pipeline

import (
	"fmt"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
)

// O worktree de uma demanda NUNCA deve ficar com trabalho nao commitado entre
// fases. Enquanto ficava, a pre-checagem da fase seguinte barrava a execucao com
// um erro de git que o usuario da UI nao tinha como resolver: um executor morto
// no meio (budget estourado, queda do servico) deixava centenas de linhas
// soltas, e todo "Retomar" falhava na hora, para sempre.
//
// A saida e commitar o que sobrou num commit de RESGUARDO marcado, em vez de
// descartar (o trabalho ja foi pago) ou de falhar (o usuario fica sem saida).
// Sao dois marcadores, com destinos diferentes:
//
//   - MarcadorParcial: trabalho da PROPRIA fase, salvo quando ela termina sem
//     concluir (falha, pausa, franquia). Como o scheduler roda uma fase por vez
//     por demanda e so libera uma fase com as depende_de concluidas, o resto so
//     pode pertencer a fase que vai reexecutar — ela recomeca em cima do proprio
//     trabalho, sem repetir (nem repagar) o que ja tinha feito. Ao concluir, o
//     commit final da fase funde esses parciais (fundirParciais), entao o
//     historico publicado continua com um commit por fase.
//
//   - MarcadorExterno: trabalho de origem DESCONHECIDA que a pre-checagem
//     encontrou — tipicamente a edicao manual de uma fase requer_humano
//     (concluir uma fase humana pela UI nao commita nada) ou o usuario mexendo
//     no worktree. Nunca e descartado nem fundido: vira commit proprio e fica.
const (
	MarcadorParcial = "[praxis-parcial]"
	MarcadorExterno = "[praxis-externo]"
)

// MaxParciaisDoTopo limita a varredura do topo da branch ao procurar commits de
// resguardo. Um teto generoso: sao no maximo alguns por fase (um por tentativa
// interrompida), e o commit da fase concluida zera a conta.
const MaxParciaisDoTopo = 50

// AssuntoParcial monta o assunto do commit de resguardo de uma fase. E a MESMA
// funcao usada para criar o commit e (via PrefixoParcial) para reconhece-lo
// depois — o formato mora num lugar so.
func AssuntoParcial(codigo, titulo string) string {
	return fmt.Sprintf("%s %s %s", PrefixoParcial(codigo), titulo, MarcadorParcial)
}

// PrefixoParcial e o inicio do assunto de um commit de resguardo da fase
// `codigo`: a chave que o commit final e o "reiniciar fase" usam para reconhecer,
// no topo da branch, os commits que podem ser desfeitos.
func PrefixoParcial(codigo string) string {
	return fmt.Sprintf("Fase %s (parcial):", codigo)
}

// ContarParciaisDoTopo conta quantos commits de resguardo da fase `codigo` estao
// no TOPO da branch em dir. Para no primeiro commit que nao for um resguardo
// dessa fase: um commit de fase concluida, ou de trabalho manual preservado
// ([praxis-externo]), nunca entra na conta — e portanto nunca e desfeito.
//
// Uma falha de leitura do git devolve 0: na duvida, nao mexe no historico.
func ContarParciaisDoTopo(dir, codigo string) int {
	assuntos, err := gitops.AssuntosDoTopo(dir, MaxParciaisDoTopo)
	if err != nil {
		return 0
	}
	prefixo := PrefixoParcial(codigo)
	n := 0
	for _, a := range assuntos {
		if !strings.HasPrefix(a, prefixo) || !strings.Contains(a, MarcadorParcial) {
			break
		}
		n++
	}
	return n
}

// commitarResiduo deixa o worktree LIMPO commitando o que houver de nao
// commitado, sob o marcador dado. Devolve se chegou a commitar.
//
// E best-effort por design: uma rede de seguranca nao pode virar um novo modo de
// falha. Se o git recusar, o erro vira aviso e o desfecho da fase segue igual —
// a proxima passada tenta de novo pela pre-checagem.
func (c *ContextoExec) commitarResiduo(f db.Fase, marcador, motivo string) bool {
	if c.Git == nil || strings.TrimSpace(c.Worktree) == "" {
		return false
	}
	limpo, err := gitops.Limpo(c.Worktree)
	if err != nil || limpo {
		return false
	}

	var assunto, tipoEvento, titulo string
	switch marcador {
	case MarcadorExterno:
		assunto = fmt.Sprintf("Trabalho manual antes da Fase %s %s", f.Codigo, MarcadorExterno)
		tipoEvento = "trabalho_manual_preservado"
		titulo = fmt.Sprintf("Praxis: trabalho manual preservado antes da Fase %s", f.Codigo)
	default:
		assunto = AssuntoParcial(f.Codigo, f.Titulo)
		tipoEvento = "parcial_preservado"
		titulo = fmt.Sprintf("Praxis: trabalho parcial da Fase %s preservado", f.Codigo)
	}

	msg := fmt.Sprintf("%s\n\n%s\n", assunto, strings.TrimSpace(motivo))
	if err := c.Git.Commit(c.Worktree, msg, c.Autor); err != nil {
		c.registrarEvento("aviso", fmt.Sprintf("Praxis: nao consegui preservar o trabalho da Fase %s", f.Codigo), err.Error())
		return false
	}
	c.registrarEvento(tipoEvento, titulo, motivo)
	return true
}

// fundirParciais desfaz os commits de resguardo desta fase que estao no topo,
// devolvendo o conteudo deles ao index para entrar no commit final — o historico
// da branch fica com UM commit por fase, como se a fase tivesse rodado de uma
// vez so. Chamado logo antes do commit de conclusao.
//
// Seguro em relacao ao remoto: o push automatico so acontece depois do commit de
// conclusao (Runner.executarFase), entao um commit parcial nunca chega ao origin
// para depois sumir. Uma falha aqui nao e fatal: a fase conclui do mesmo jeito,
// so deixa os parciais visiveis no historico.
func (c *ContextoExec) fundirParciais(f db.Fase) {
	if c.Git == nil || strings.TrimSpace(c.Worktree) == "" {
		return
	}
	n := ContarParciaisDoTopo(c.Worktree, f.Codigo)
	if n == 0 {
		return
	}
	if err := c.Git.DesfazerCommitsDoTopo(c.Worktree, n, true); err != nil {
		c.registrarEvento("aviso", fmt.Sprintf("Praxis: nao consegui fundir o trabalho parcial da Fase %s", f.Codigo), err.Error())
	}
}
