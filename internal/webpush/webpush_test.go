package webpush

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Vetor do Apêndice A da RFC 8291.
const (
	rfcTexto     = "When I grow up, I want to be a watermelon"
	rfcUAPrivada = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfcUAPublica = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcAuth      = "BTBZMqHH6r4Tts7J_aSIgg"
	rfcASPrivada = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcSalt      = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcSaida     = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func decode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64.DecodeString(s)
	if err != nil {
		t.Fatalf("base64 %q: %v", s, err)
	}
	return b
}

func TestCifrarReproduzVetorDaRFC8291(t *testing.T) {
	asPriv, err := ecdh.P256().NewPrivateKey(decode(t, rfcASPrivada))
	if err != nil {
		t.Fatal(err)
	}
	saida, err := cifrarCom([]byte(rfcTexto), Assinante{P256dh: rfcUAPublica, Auth: rfcAuth}, decode(t, rfcSalt), asPriv)
	if err != nil {
		t.Fatalf("cifrar: %v", err)
	}
	if got := b64.EncodeToString(saida); got != rfcSaida {
		t.Fatalf("saída difere do vetor da RFC:\n got %s\nquer %s", got, rfcSaida)
	}
}

// decifrar é o lado do navegador (só para o teste de ida e volta).
func decifrar(t *testing.T, corpo []byte, uaPriv *ecdh.PrivateKey, auth []byte) []byte {
	t.Helper()
	salt := corpo[:16]
	rs := binary.BigEndian.Uint32(corpo[16:20])
	idlen := int(corpo[20])
	asPubBytes := corpo[21 : 21+idlen]
	cifrado := corpo[21+idlen:]
	if rs != tamanhoRegistro {
		t.Fatalf("rs = %d", rs)
	}
	asPub, err := ecdh.P256().NewPublicKey(asPubBytes)
	if err != nil {
		t.Fatal(err)
	}
	segredo, err := uaPriv.ECDH(asPub)
	if err != nil {
		t.Fatal(err)
	}
	prkChave, _ := hkdf.Extract(sha256.New, segredo, auth)
	ikm, _ := hkdf.Expand(sha256.New, prkChave, "WebPush: info\x00"+string(uaPriv.PublicKey().Bytes())+string(asPubBytes), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	bloco, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(bloco)
	claro, err := gcm.Open(nil, nonce, cifrado, nil)
	if err != nil {
		t.Fatalf("abrir: %v", err)
	}
	if claro[len(claro)-1] != 0x02 {
		t.Fatalf("delimitador = %x", claro[len(claro)-1])
	}
	return claro[:len(claro)-1]
}

func TestCifrarIdaEVoltaComChavesNovas(t *testing.T) {
	uaPriv, err := ecdh.P256().GenerateKey(strings.NewReader(strings.Repeat("x", 64)))
	if err != nil {
		t.Fatal(err)
	}
	auth := []byte("0123456789abcdef")
	ass := Assinante{P256dh: b64.EncodeToString(uaPriv.PublicKey().Bytes()), Auth: b64.EncodeToString(auth)}
	payload := []byte(`{"id":7,"titulo":"Consulta respondida","rota":"#consultas/7"}`)
	corpo, err := Cifrar(payload, ass)
	if err != nil {
		t.Fatalf("cifrar: %v", err)
	}
	if got := decifrar(t, corpo, uaPriv, auth); string(got) != string(payload) {
		t.Fatalf("ida e volta: %q", got)
	}
	// dois envios do mesmo payload diferem (salt e chave efêmera aleatórios).
	corpo2, _ := Cifrar(payload, ass)
	if string(corpo) == string(corpo2) {
		t.Fatal("cifra deveria variar a cada envio")
	}
	if _, err := Cifrar(make([]byte, 5000), ass); err != ErrPayloadGrande {
		t.Fatalf("payload grande: %v", err)
	}
	if _, err := Cifrar(payload, Assinante{P256dh: "lixo", Auth: ass.Auth}); err == nil {
		t.Fatal("p256dh inválido deveria falhar")
	}
}

func TestVAPIDAssinaEVerifica(t *testing.T) {
	ch, err := GerarChaves()
	if err != nil {
		t.Fatal(err)
	}
	if len(decode(t, ch.Publica)) != 65 || len(decode(t, ch.Privada)) != 32 {
		t.Fatalf("tamanhos: pub %d priv %d", len(decode(t, ch.Publica)), len(decode(t, ch.Privada)))
	}
	tok, err := AssinarVAPID(ch, "https://push.example", "mailto:admin@x.com", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	partes := strings.Split(tok, ".")
	if len(partes) != 3 {
		t.Fatalf("jwt com %d partes", len(partes))
	}
	var cab map[string]string
	_ = json.Unmarshal(decode(t, partes[0]), &cab)
	if cab["alg"] != "ES256" || cab["typ"] != "JWT" {
		t.Fatalf("cabeçalho: %v", cab)
	}
	var claims map[string]any
	_ = json.Unmarshal(decode(t, partes[1]), &claims)
	if claims["aud"] != "https://push.example" || claims["sub"] != "mailto:admin@x.com" {
		t.Fatalf("claims: %v", claims)
	}
	exp := int64(claims["exp"].(float64))
	if exp < time.Now().Add(59*time.Minute).Unix() || exp > time.Now().Add(61*time.Minute).Unix() {
		t.Fatalf("exp = %d", exp)
	}
	// verifica a assinatura com a chave pública (como o serviço de push faz).
	pub := decode(t, ch.Publica)
	chave := &ecdsa.PublicKey{Curve: ecdhCurva(), X: new(big.Int).SetBytes(pub[1:33]), Y: new(big.Int).SetBytes(pub[33:])}
	hash := sha256.Sum256([]byte(partes[0] + "." + partes[1]))
	sig := decode(t, partes[2])
	if len(sig) != 64 || !ecdsa.Verify(chave, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("assinatura ES256 não verifica")
	}
	// validade acima de 24h cai em 12h.
	tok2, _ := AssinarVAPID(ch, "https://x", "mailto:a", 48*time.Hour)
	_ = json.Unmarshal(decode(t, strings.Split(tok2, ".")[1]), &claims)
	if e := int64(claims["exp"].(float64)); e > time.Now().Add(13*time.Hour).Unix() {
		t.Fatalf("exp acima de 24h deveria cair em 12h: %d", e)
	}
}

func TestEnviarPostaComCabecalhosVAPID(t *testing.T) {
	ch, _ := GerarChaves()
	uaPriv, _ := ecdh.P256().GenerateKey(strings.NewReader(strings.Repeat("y", 64)))
	auth := []byte("fedcba9876543210")
	var recebido struct {
		auth, enc, ttl, urg, topico string
		corpo                       []byte
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recebido.auth = r.Header.Get("Authorization")
		recebido.enc = r.Header.Get("Content-Encoding")
		recebido.ttl = r.Header.Get("TTL")
		recebido.urg = r.Header.Get("Urgency")
		recebido.topico = r.Header.Get("Topic")
		recebido.corpo, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer ts.Close()

	ass := Assinante{Endpoint: ts.URL + "/push/abc", P256dh: b64.EncodeToString(uaPriv.PublicKey().Bytes()), Auth: b64.EncodeToString(auth)}
	status, err := Enviar(context.Background(), ts.Client(), ch, "mailto:ops@x.com", ass, []byte("oi"), Opcoes{Topico: "consulta_respondida"})
	if err != nil || status != http.StatusCreated {
		t.Fatalf("enviar: status %d err %v", status, err)
	}
	if !strings.HasPrefix(recebido.auth, "vapid t=") || !strings.Contains(recebido.auth, ", k="+ch.Publica) {
		t.Fatalf("Authorization = %q", recebido.auth)
	}
	if recebido.enc != "aes128gcm" || recebido.ttl != "86400" || recebido.urg != "normal" || recebido.topico != "consulta_respondida" {
		t.Fatalf("cabeçalhos: %+v", recebido)
	}
	if got := decifrar(t, recebido.corpo, uaPriv, auth); string(got) != "oi" {
		t.Fatalf("corpo decifrado = %q", got)
	}
	if !AssinaturaMorta(410) || !AssinaturaMorta(404) || AssinaturaMorta(429) {
		t.Fatal("AssinaturaMorta")
	}
	if _, err := Enviar(context.Background(), ts.Client(), ch, "mailto:a", Assinante{Endpoint: "nada", P256dh: ass.P256dh, Auth: ass.Auth}, []byte("x"), Opcoes{}); err == nil {
		t.Fatal("endpoint inválido deveria falhar")
	}
}

// ecdhCurva devolve a curva P-256 do pacote elliptic (para verificar ES256).
func ecdhCurva() elliptic.Curve { return elliptic.P256() }
