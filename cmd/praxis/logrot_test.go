package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArquivoRotativo(t *testing.T) {
	dir := t.TempDir()
	caminho := filepath.Join(dir, "sub", "servico.log")
	a, err := abrirArquivoRotativo(caminho, 18, 2)
	if err != nil {
		t.Fatalf("abrir: %v", err)
	}
	defer a.Close()

	// 3 registros de 9 bytes: o 3º não cabe (18+9 > 18) → rotaciona antes.
	for i := 0; i < 3; i++ {
		if _, err := a.Write([]byte("linha-0" + string(rune('0'+i)) + "\n")); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	corrente, _ := os.ReadFile(caminho)
	if string(corrente) != "linha-02\n" {
		t.Fatalf("corrente = %q, quero só o 3º registro", corrente)
	}
	anterior, _ := os.ReadFile(caminho + ".1")
	if string(anterior) != "linha-00\nlinha-01\n" {
		t.Fatalf(".1 = %q", anterior)
	}

	// Mais duas rotações: .1 → .2, .2 some (manter=2).
	for i := 3; i < 7; i++ {
		if _, err := a.Write([]byte("linha-0" + string(rune('0'+i)) + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	// 03 cabe (9+9=18); 04 rotaciona; 05 cabe; 06 rotaciona → corrente "06",
	// .1 = "04,05", .2 = "02,03", e "00,01" (seria .3) descartado.
	corrente, _ = os.ReadFile(caminho)
	if string(corrente) != "linha-06\n" {
		t.Fatalf("corrente após rotações = %q", corrente)
	}
	if b, _ := os.ReadFile(caminho + ".1"); string(b) != "linha-04\nlinha-05\n" {
		t.Fatalf(".1 após rotações = %q", b)
	}
	if b, _ := os.ReadFile(caminho + ".2"); string(b) != "linha-02\nlinha-03\n" {
		t.Fatalf(".2 após rotações = %q", b)
	}
	if _, err := os.Stat(caminho + ".3"); !os.IsNotExist(err) {
		t.Fatalf(".3 não deveria existir (manter=2)")
	}
}

// TestArquivoRotativoReabreEmAppend: reabrir o mesmo arquivo continua de onde
// parou (o tamanho inicial vem do Stat) e um registro nunca é partido.
func TestArquivoRotativoReabreEmAppend(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "s.log")
	a, err := abrirArquivoRotativo(caminho, 0, 1) // sem rotação
	if err != nil {
		t.Fatal(err)
	}
	a.Write([]byte(strings.Repeat("x", 100)))
	a.Close()

	b, err := abrirArquivoRotativo(caminho, 150, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.tamanho != 100 {
		t.Fatalf("tamanho inicial = %d, quero 100", b.tamanho)
	}
	b.Write([]byte(strings.Repeat("y", 60))) // 160 > 150 → rotaciona
	if got, _ := os.ReadFile(caminho); len(got) != 60 || got[0] != 'y' {
		t.Fatalf("corrente = %d bytes (%q…)", len(got), got[:1])
	}
	if got, _ := os.ReadFile(caminho + ".1"); len(got) != 100 {
		t.Fatalf(".1 = %d bytes, quero 100", len(got))
	}
}
