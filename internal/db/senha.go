package db

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Hash de senha com PBKDF2-HMAC-SHA256 (stdlib, sem dependência externa). O
// formato persistido é self-describing para permitir aumentar o custo no futuro
// sem migração: "pbkdf2-sha256$<iter>$<salt-b64>$<hash-b64>".
const (
	pbkdf2Iteracoes = 210_000 // recomendação OWASP para PBKDF2-HMAC-SHA256
	pbkdf2TamSal    = 16      // bytes
	pbkdf2TamChave  = 32      // bytes (256 bits)
	pbkdf2Prefixo   = "pbkdf2-sha256"
)

// ErrSenhaVazia é devolvido por HashSenha quando a senha é vazia após trim.
var ErrSenhaVazia = errors.New("senha não pode ser vazia")

// HashSenha deriva o hash PBKDF2 de uma senha em claro e devolve a string
// self-describing pronta para persistir em users.senha_hash.
func HashSenha(senha string) (string, error) {
	if strings.TrimSpace(senha) == "" {
		return "", ErrSenhaVazia
	}
	sal := make([]byte, pbkdf2TamSal)
	if _, err := rand.Read(sal); err != nil {
		return "", fmt.Errorf("gerar sal: %w", err)
	}
	dk, err := pbkdf2.Key(sha256.New, senha, sal, pbkdf2Iteracoes, pbkdf2TamChave)
	if err != nil {
		return "", fmt.Errorf("derivar senha: %w", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s", pbkdf2Prefixo, pbkdf2Iteracoes,
		base64.RawStdEncoding.EncodeToString(sal),
		base64.RawStdEncoding.EncodeToString(dk)), nil
}

// VerificarSenha confere uma senha em claro contra o hash persistido. Devolve
// true só quando batem. Hash malformado → false (não é erro do chamador; um hash
// corrompido simplesmente nunca autentica). A comparação é em tempo constante.
func VerificarSenha(hashPersistido, senha string) bool {
	partes := strings.Split(hashPersistido, "$")
	if len(partes) != 4 || partes[0] != pbkdf2Prefixo {
		return false
	}
	iter, err := strconv.Atoi(partes[1])
	if err != nil || iter <= 0 {
		return false
	}
	sal, err := base64.RawStdEncoding.DecodeString(partes[2])
	if err != nil {
		return false
	}
	esperado, err := base64.RawStdEncoding.DecodeString(partes[3])
	if err != nil {
		return false
	}
	dk, err := pbkdf2.Key(sha256.New, senha, sal, iter, len(esperado))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(dk, esperado) == 1
}
