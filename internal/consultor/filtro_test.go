package consultor

import (
	"strings"
	"testing"
)

func TestSanitizarProsaPuraPassaIntacta(t *testing.T) {
	md := `## Como funciona a baixa de títulos

Quando o cliente paga um boleto, o sistema localiza o título em aberto pela
linha digitável e registra a baixa. Se o valor pago for menor que o valor do
título, a rotina de Baixa Parcial cria um título residual.

- A baixa dispara a atualização do saldo do cliente.
- O título baixado sai da tela de Cobrança Pendente.`

	limpo, redigidos, recusar := Sanitizar(md)
	if recusar {
		t.Fatal("prosa pura não deveria ser recusada")
	}
	if redigidos != 0 {
		t.Fatalf("redigidos = %d, quero 0", redigidos)
	}
	if limpo != md {
		t.Fatalf("prosa alterada:\n%s", limpo)
	}
}

func TestSanitizarRemoveBlocoCercado(t *testing.T) {
	md := "A rotina funciona assim:\n\n```go\nfunc Baixar(t Titulo) error {\n\treturn db.Baixa(t.ID)\n}\n```\n\nDepois disso o saldo é atualizado."
	limpo, redigidos, recusar := Sanitizar(md)
	if recusar {
		t.Fatalf("não deveria recusar (só um bloco): %q", limpo)
	}
	if redigidos == 0 {
		t.Fatal("bloco cercado não foi redigido")
	}
	if strings.Contains(limpo, "func Baixar") || strings.Contains(limpo, "```") {
		t.Fatalf("código vazou:\n%s", limpo)
	}
	if !strings.Contains(limpo, marcadorRedacao) {
		t.Fatalf("marcador de redação ausente:\n%s", limpo)
	}
	if !strings.Contains(limpo, "saldo é atualizado") {
		t.Fatalf("prosa em volta foi perdida:\n%s", limpo)
	}
}

func TestSanitizarCercaSemFechamentoRedigeAteOFim(t *testing.T) {
	md := "Explicação:\n```\nSELECT * FROM titulos WHERE status = 'aberto'\nUPDATE titulos SET status = 'baixado'"
	limpo, _, _ := Sanitizar(md)
	if strings.Contains(limpo, "SELECT") || strings.Contains(limpo, "UPDATE") {
		t.Fatalf("cerca malformada vazou código:\n%s", limpo)
	}
}

func TestSanitizarCodigoCruSemCerca(t *testing.T) {
	md := `A validação é feita assim:

func ValidarCPF(cpf string) bool {
	if len(cpf) != 11 {
		return false
	}
	return digitosConferem(cpf)
}`
	limpo, redigidos, _ := Sanitizar(md)
	if redigidos == 0 {
		t.Fatal("código cru sem cerca não foi detectado")
	}
	if strings.Contains(limpo, "ValidarCPF") || strings.Contains(limpo, "return false") {
		t.Fatalf("código cru vazou:\n%s", limpo)
	}
}

func TestSanitizarSQLCruSemCerca(t *testing.T) {
	md := "O relatório roda estas consultas:\n\nSELECT id, valor FROM titulos WHERE vencimento < date('now')\nINSERT INTO log_cobranca (titulo_id) VALUES (1)\nUPDATE titulos SET notificado = 1 WHERE id = 9"
	limpo, redigidos, _ := Sanitizar(md)
	if redigidos == 0 {
		t.Fatal("SQL cru não foi detectado")
	}
	if strings.Contains(strings.ToUpper(limpo), "SELECT ID") {
		t.Fatalf("SQL vazou:\n%s", limpo)
	}
}

func TestSanitizarInlineCurtoPreservadoLongoRedigido(t *testing.T) {
	longo := "`" + strings.Repeat("x := calcula(y); ", 10) + "`"
	md := "A rotina `BaixaDeTitulos` chama a tela `Cobranca`. Exemplo: " + longo
	limpo, redigidos, recusar := Sanitizar(md)
	if recusar {
		t.Fatal("não deveria recusar")
	}
	if !strings.Contains(limpo, "`BaixaDeTitulos`") || !strings.Contains(limpo, "`Cobranca`") {
		t.Fatalf("nomes curtos de rotina foram redigidos:\n%s", limpo)
	}
	if redigidos == 0 || strings.Contains(limpo, "calcula(y)") {
		t.Fatalf("inline longo vazou:\n%s", limpo)
	}
}

func TestSanitizarTabelaEListaNaoSaoCodigo(t *testing.T) {
	md := `| Rotina | O que faz |
|---|---|
| Baixa | Liquida o título |
| Estorno | Desfaz a baixa |

- item um (a) e (b)
- item dois {com chaves na prosa}
* item três`
	limpo, redigidos, recusar := Sanitizar(md)
	if recusar || redigidos != 0 {
		t.Fatalf("tabela/lista markdown tratada como código (redigidos=%d, recusar=%v):\n%s",
			redigidos, recusar, limpo)
	}
}

func TestSanitizarRespostaMajoritariamenteCodigoRecusa(t *testing.T) {
	md := "```go\n" + strings.Repeat("x := 1\n", 30) + "```\nok."
	_, _, recusar := Sanitizar(md)
	if !recusar {
		t.Fatal("resposta quase toda código deveria ser recusada (fail-closed)")
	}
}

func TestSanitizarDuasLinhasSuspeitasNaoRedige(t *testing.T) {
	// Menos de 3 linhas consecutivas com cara de código: tolerado (evita
	// falso-positivo em prosa com pontuação densa).
	md := "O fluxo é: pedido -> separação e depois:\nconferência -> expedição\n\nE termina na entrega."
	limpo, _, recusar := Sanitizar(md)
	if recusar {
		t.Fatal("não deveria recusar")
	}
	if !strings.Contains(limpo, "pedido") {
		t.Fatalf("prosa perdida:\n%s", limpo)
	}
}
