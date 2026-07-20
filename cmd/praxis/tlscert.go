package main

// TLS do serve: com -tls-cert/-tls-key usa o certificado do operador; com -tls
// (sem cert próprio) gera e reutiliza um autoassinado em PRAXIS_HOME/tls. O
// HTTPS não é cosmético aqui: o workbench do VS Code Web (proxy /ide/*) exige
// contexto seguro no navegador (service workers/crypto.subtle), então acesso
// remoto pela rede só funciona com TLS — para uso local, o loopback dispensa.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// validadeCertAuto é a validade do certificado autoassinado. Longa de propósito:
// ele não é publicamente confiável de qualquer forma (o navegador avisa uma vez)
// e regenerar mudaria a impressão digital que os usuários já aceitaram.
const validadeCertAuto = 10 * 365 * 24 * time.Hour

// configTLS resolve a configuração TLS do serve. Precedência: certificado
// próprio (-tls-cert/-tls-key) > autoassinado (-tls) > sem TLS (nil).
func configTLS(certArq, keyArq string, autoTLS bool, dirTLS string, logf func(msg string, kv ...any)) (*tls.Config, error) {
	var cert tls.Certificate
	var err error
	switch {
	case certArq != "" || keyArq != "":
		if certArq == "" || keyArq == "" {
			return nil, fmt.Errorf("-tls-cert e -tls-key devem ser informados juntos")
		}
		cert, err = tls.LoadX509KeyPair(certArq, keyArq)
		if err != nil {
			return nil, fmt.Errorf("carregar certificado TLS: %w", err)
		}
	case autoTLS:
		cert, err = certificadoAutoassinado(dirTLS, logf)
		if err != nil {
			return nil, fmt.Errorf("certificado autoassinado: %w", err)
		}
	default:
		return nil, nil
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

// certificadoAutoassinado carrega o par cert.pem/key.pem de dir, gerando-o na
// primeira vez (ECDSA P-256, SANs com localhost, hostname e os IPs da máquina).
func certificadoAutoassinado(dir string, logf func(msg string, kv ...any)) (tls.Certificate, error) {
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	if _, err := os.Stat(certPath); err == nil {
		return tls.LoadX509KeyPair(certPath, keyPath)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, err
	}
	chave, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	// IsCA + CertSign: o certificado é a própria âncora de confiança, para poder
	// ser INSTALADO como confiável nos dispositivos (GET /cert). Só "aceitar o
	// risco" no aviso do navegador não basta: o Chrome aplica a exceção à página
	// e aos assets, mas RECUSA o certificado nas conexões WebSocket — e o IDE
	// web depende delas.
	molde := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "praxis"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(validadeCertAuto),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	if nome, err := os.Hostname(); err == nil && nome != "" {
		molde.DNSNames = append(molde.DNSNames, nome)
	}
	// Inclui os IPs atuais da máquina para o navegador poder validar o SAN ao
	// acessar por IP na LAN. IPs adicionados depois exigem apagar PRAXIS_HOME/tls
	// para regenerar (documentado no README).
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() {
				molde.IPAddresses = append(molde.IPAddresses, ipn.IP)
			}
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, &molde, &molde, &chave.PublicKey, chave)
	if err != nil {
		return tls.Certificate{}, err
	}
	chaveDER, err := x509.MarshalECPrivateKey(chave)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: chaveDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	logf("certificado TLS autoassinado gerado", "caminho", certPath, "sans", len(molde.DNSNames)+len(molde.IPAddresses))
	return tls.X509KeyPair(certPEM, keyPEM)
}
