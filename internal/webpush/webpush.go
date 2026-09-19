// Package webpush envia notificações Web Push só com a stdlib (M4 do
// PLANO_INTERNET): VAPID (RFC 8292 — o servidor se identifica ao serviço de
// push com um JWT ES256) e a cifra do payload no esquema aes128gcm (RFC 8291,
// que aplica a RFC 8188 sobre ECDH P-256 + HKDF + AES-128-GCM). O vetor de
// teste do Apêndice A da RFC 8291 é reproduzido byte a byte em webpush_test.go.
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var b64 = base64.RawURLEncoding

// tamanhoRegistro é o rs do cabeçalho aes128gcm. O payload inteiro vai num
// único registro; o limite prático dos serviços de push é ~4 KB.
const tamanhoRegistro = 4096

// ErrPayloadGrande: o payload cifrado não cabe num registro.
var ErrPayloadGrande = errors.New("webpush: payload maior que o registro (4 KB)")

// Chaves é o par VAPID da instância, em base64url: Publica é o ponto P-256 sem
// compressão (65 bytes, o que o navegador recebe como applicationServerKey);
// Privada é o escalar (32 bytes).
type Chaves struct {
	Publica string
	Privada string
}

// GerarChaves cria um par VAPID novo.
func GerarChaves() (Chaves, error) {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return Chaves{}, fmt.Errorf("webpush: gerar chave: %w", err)
	}
	return Chaves{
		Publica: b64.EncodeToString(priv.PublicKey().Bytes()),
		Privada: b64.EncodeToString(priv.Bytes()),
	}, nil
}

// chaveECDSA reconstrói a chave privada ECDSA (P-256) a partir do escalar
// base64url — sem usar as funções obsoletas de crypto/elliptic.
func chaveECDSA(privada string) (*ecdsa.PrivateKey, error) {
	d, err := b64.DecodeString(privada)
	if err != nil {
		return nil, fmt.Errorf("webpush: chave privada inválida: %w", err)
	}
	k, err := ecdh.P256().NewPrivateKey(d)
	if err != nil {
		return nil, fmt.Errorf("webpush: chave privada inválida: %w", err)
	}
	pub := k.PublicKey().Bytes() // 0x04 || X || Y
	if len(pub) != 65 || pub[0] != 4 {
		return nil, errors.New("webpush: ponto público inesperado")
	}
	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(pub[1:33]),
			Y:     new(big.Int).SetBytes(pub[33:]),
		},
		D: new(big.Int).SetBytes(d),
	}, nil
}

// AssinarVAPID emite o JWT ES256 do VAPID: aud é a origem do endpoint de push
// (esquema://host), sub o contato do operador ("mailto:…"), validade no máximo
// 24 h (a RFC exige). Devolve o token compacto.
func AssinarVAPID(ch Chaves, aud, sub string, validade time.Duration) (string, error) {
	if validade <= 0 || validade > 24*time.Hour {
		validade = 12 * time.Hour
	}
	priv, err := chaveECDSA(ch.Privada)
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{
		"aud": aud,
		"exp": time.Now().Add(validade).Unix(),
		"sub": sub,
	})
	if err != nil {
		return "", err
	}
	entrada := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`)) + "." + b64.EncodeToString(claims)
	hash := sha256.Sum256([]byte(entrada))
	r, s, err := ecdsa.Sign(rand.Reader, priv, hash[:])
	if err != nil {
		return "", fmt.Errorf("webpush: assinar vapid: %w", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return entrada + "." + b64.EncodeToString(sig), nil
}

// Assinante são os dados de uma PushSubscription do navegador: o endpoint e as
// chaves `p256dh` (ponto P-256 do dispositivo) e `auth` (segredo de 16 bytes),
// ambas em base64url como o navegador as entrega.
type Assinante struct {
	Endpoint string
	P256dh   string
	Auth     string
}

// Cifrar produz o corpo aes128gcm (cabeçalho + registro cifrado) do payload
// para o assinante, com salt e chave efêmera aleatórios.
func Cifrar(payload []byte, ass Assinante) ([]byte, error) {
	return cifrarCom(payload, ass, nil, nil)
}

// cifrarCom é Cifrar com salt e chave do servidor injetáveis (vetor de teste).
func cifrarCom(payload []byte, ass Assinante, salt []byte, asPriv *ecdh.PrivateKey) ([]byte, error) {
	uaPubBytes, err := b64.DecodeString(ass.P256dh)
	if err != nil {
		return nil, fmt.Errorf("webpush: p256dh inválido: %w", err)
	}
	auth, err := b64.DecodeString(ass.Auth)
	if err != nil || len(auth) == 0 {
		return nil, errors.New("webpush: auth inválido")
	}
	curva := ecdh.P256()
	uaPub, err := curva.NewPublicKey(uaPubBytes)
	if err != nil {
		return nil, fmt.Errorf("webpush: p256dh inválido: %w", err)
	}
	if asPriv == nil {
		if asPriv, err = curva.GenerateKey(rand.Reader); err != nil {
			return nil, fmt.Errorf("webpush: chave efêmera: %w", err)
		}
	}
	if salt == nil {
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return nil, fmt.Errorf("webpush: salt: %w", err)
		}
	}
	asPub := asPriv.PublicKey().Bytes()
	segredo, err := asPriv.ECDH(uaPub)
	if err != nil {
		return nil, fmt.Errorf("webpush: ecdh: %w", err)
	}

	// RFC 8291 §3.3/§3.4: IKM = HKDF(auth, ecdh_secret, "WebPush: info" || ua_pub || as_pub)
	prkChave, err := hkdf.Extract(sha256.New, segredo, auth)
	if err != nil {
		return nil, err
	}
	ikm, err := hkdf.Expand(sha256.New, prkChave, "WebPush: info\x00"+string(uaPubBytes)+string(asPub), 32)
	if err != nil {
		return nil, err
	}
	// RFC 8188 §2: CEK e NONCE derivados de PRK = HKDF-Extract(salt, IKM).
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}

	bloco, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(bloco)
	if err != nil {
		return nil, err
	}
	// Um único registro: payload + delimitador 0x02 (último registro), sem padding.
	registro := append(append([]byte{}, payload...), 0x02)
	cifrado := gcm.Seal(nil, nonce, registro, nil)
	if len(cifrado) > tamanhoRegistro {
		return nil, ErrPayloadGrande
	}

	// Cabeçalho (RFC 8188 §2.1): salt(16) | rs(4, big-endian) | idlen(1) | keyid(as_pub).
	var corpo bytes.Buffer
	corpo.Write(salt)
	_ = binary.Write(&corpo, binary.BigEndian, uint32(tamanhoRegistro))
	corpo.WriteByte(byte(len(asPub)))
	corpo.Write(asPub)
	corpo.Write(cifrado)
	return corpo.Bytes(), nil
}

// Opcoes ajusta um envio: TTL em segundos que o serviço de push guarda a
// mensagem se o dispositivo estiver offline (default 86400), Urgencia
// (very-low | low | normal | high; default normal) e Topico (mensagens com o
// mesmo tópico colapsam no serviço: só a última chega).
type Opcoes struct {
	TTL      int
	Urgencia string
	Topico   string
}

// Enviar cifra o payload e o posta no endpoint do assinante com o VAPID.
// Devolve o status HTTP do serviço de push (201/200 = aceito; 404/410 =
// assinatura morta, descarte; 413 = grande demais; 429/5xx = tente depois).
// Erro só em falha de cifra/assinatura/transporte.
func Enviar(ctx context.Context, cli *http.Client, ch Chaves, contato string, ass Assinante, payload []byte, op Opcoes) (int, error) {
	if cli == nil {
		cli = &http.Client{Timeout: 10 * time.Second}
	}
	corpo, err := Cifrar(payload, ass)
	if err != nil {
		return 0, err
	}
	u, err := url.Parse(strings.TrimSpace(ass.Endpoint))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return 0, errors.New("webpush: endpoint inválido")
	}
	jwt, err := AssinarVAPID(ch, u.Scheme+"://"+u.Host, contato, 12*time.Hour)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(corpo))
	if err != nil {
		return 0, err
	}
	ttl := op.TTL
	if ttl <= 0 {
		ttl = 86400
	}
	urg := op.Urgencia
	if urg == "" {
		urg = "normal"
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", strconv.Itoa(ttl))
	req.Header.Set("Urgency", urg)
	if op.Topico != "" {
		req.Header.Set("Topic", op.Topico)
	}
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+ch.Publica)
	resp, err := cli.Do(req)
	if err != nil {
		return 0, fmt.Errorf("webpush: enviar: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// AssinaturaMorta informa se o status do serviço de push significa que a
// assinatura não existe mais (o dispositivo cancelou ou expirou): descarte-a.
func AssinaturaMorta(status int) bool {
	return status == http.StatusNotFound || status == http.StatusGone
}
