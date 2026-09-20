package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// arquivoRotativo é um io.Writer que grava em um arquivo e o rotaciona por
// tamanho (caminho → caminho.1 → caminho.2 …), mantendo as `manter` cópias mais
// recentes. Usado para o log do serviço no Windows, onde não há journald e a
// rotação é responsabilidade do programa (D8 da ADR 0001). Seguro para uso
// concorrente.
type arquivoRotativo struct {
	mu       sync.Mutex
	caminho  string
	maxBytes int64
	manter   int
	f        *os.File
	tamanho  int64
}

// abrirArquivoRotativo abre (ou cria, em modo append) o arquivo em caminho,
// criando a pasta se preciso. maxBytes <= 0 desliga a rotação; manter < 1
// mantém apenas o arquivo corrente (o anterior é descartado ao rotacionar).
func abrirArquivoRotativo(caminho string, maxBytes int64, manter int) (*arquivoRotativo, error) {
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return nil, fmt.Errorf("criar pasta do log: %w", err)
	}
	a := &arquivoRotativo{caminho: caminho, maxBytes: maxBytes, manter: manter}
	if err := a.abrir(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *arquivoRotativo) abrir() error {
	f, err := os.OpenFile(a.caminho, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("abrir log %q: %w", a.caminho, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	a.f, a.tamanho = f, info.Size()
	return nil
}

// Write implementa io.Writer. Rotaciona antes de gravar quando a escrita
// ultrapassaria maxBytes (um registro nunca é partido entre dois arquivos).
func (a *arquivoRotativo) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.maxBytes > 0 && a.tamanho > 0 && a.tamanho+int64(len(p)) > a.maxBytes {
		if err := a.rotacionar(); err != nil {
			// Falha ao rotacionar não pode derrubar o serviço: segue gravando no
			// arquivo corrente.
			fmt.Fprintf(a.f, "logrot: falha ao rotacionar: %v\n", err)
		}
	}
	n, err := a.f.Write(p)
	a.tamanho += int64(n)
	return n, err
}

// rotacionar fecha o arquivo corrente, desloca as cópias (.k → .k+1, a mais
// velha some) e reabre um arquivo vazio.
func (a *arquivoRotativo) rotacionar() error {
	if err := a.f.Close(); err != nil {
		return err
	}
	if a.manter < 1 {
		_ = os.Remove(a.caminho)
	} else {
		_ = os.Remove(fmt.Sprintf("%s.%d", a.caminho, a.manter))
		for k := a.manter - 1; k >= 1; k-- {
			_ = os.Rename(fmt.Sprintf("%s.%d", a.caminho, k), fmt.Sprintf("%s.%d", a.caminho, k+1))
		}
		if err := os.Rename(a.caminho, a.caminho+".1"); err != nil {
			// Reabre o corrente mesmo assim para não perder o writer.
			_ = a.abrir()
			return err
		}
	}
	return a.abrir()
}

// Close fecha o arquivo corrente.
func (a *arquivoRotativo) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.f.Close()
}
