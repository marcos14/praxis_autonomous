package main

// Listener misto do serve com TLS: navegadores tentam http:// por padrão, e uma
// porta só-TLS responde a isso com um erro de handshake criptográfico ("client
// sent an HTTP request to an HTTPS server") — inútil para o usuário. Este
// listener espia o primeiro byte de cada conexão: 0x16 (ClientHello) segue o
// caminho TLS normal; qualquer outra coisa é tratada como HTTP puro e recebe um
// redirect 307 para https:// no mesmo host, fechando a conexão em seguida. As
// rotas do servidor NUNCA são servidas sem TLS — só o redirect.

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// prazoPrimeiroByte limita a espera pelo primeiro byte da conexão, para uma
// conexão muda não prender a classificação (a espiada roda em goroutine própria,
// então também não prende o Accept).
const prazoPrimeiroByte = 5 * time.Second

// listenerMisto implementa net.Listener entregando ao http.Server apenas
// conexões já embrulhadas em TLS. Conexões HTTP puras são respondidas com o
// redirect e nunca chegam ao servidor.
type listenerMisto struct {
	interno net.Listener
	cfg     *tls.Config
	conns   chan net.Conn
	erros   chan error
	done    chan struct{}
	umaVez  sync.Once
}

// novoListenerMisto envolve ln com a detecção HTTP→HTTPS e o handshake TLS.
func novoListenerMisto(ln net.Listener, cfg *tls.Config) net.Listener {
	l := &listenerMisto{
		interno: ln,
		cfg:     cfg,
		conns:   make(chan net.Conn),
		erros:   make(chan error, 1),
		done:    make(chan struct{}),
	}
	go l.aceitar()
	return l
}

func (l *listenerMisto) aceitar() {
	for {
		c, err := l.interno.Accept()
		if err != nil {
			select {
			case l.erros <- err:
			case <-l.done:
			}
			return
		}
		go l.classificar(c)
	}
}

// classificar espia o primeiro byte e roteia a conexão: TLS para o servidor,
// HTTP puro para o redirect. Conexões mudas ou com erro são fechadas.
func (l *listenerMisto) classificar(c net.Conn) {
	br := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(prazoPrimeiroByte))
	primeiro, err := br.Peek(1)
	if err != nil {
		c.Close()
		return
	}
	_ = c.SetReadDeadline(time.Time{})

	conn := &connBufferizada{Conn: c, r: br}
	// 0x16 é o record type de handshake do TLS — nenhum método HTTP começa assim.
	if primeiro[0] == 0x16 {
		select {
		case l.conns <- tls.Server(conn, l.cfg):
		case <-l.done:
			c.Close()
		}
		return
	}
	redirecionarParaHTTPS(conn, br)
	c.Close()
}

func (l *listenerMisto) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case err := <-l.erros:
		return nil, err
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *listenerMisto) Close() error {
	l.umaVez.Do(func() { close(l.done) })
	return l.interno.Close()
}

func (l *listenerMisto) Addr() net.Addr { return l.interno.Addr() }

// connBufferizada devolve à leitura os bytes já espiados pelo bufio.Reader.
type connBufferizada struct {
	net.Conn
	r *bufio.Reader
}

func (c *connBufferizada) Read(p []byte) (int, error) { return c.r.Read(p) }

// redirecionarParaHTTPS lê a requisição HTTP pura o suficiente para descobrir
// Host e caminho, e responde 307 para o mesmo endereço em https. 307 (e não
// 301/308) de propósito: navegadores fazem cache de redirect permanente, o que
// quebraria um futuro serve sem TLS no mesmo host:porta.
func redirecionarParaHTTPS(c net.Conn, br *bufio.Reader) {
	req, err := http.ReadRequest(br)
	if err != nil || req.Host == "" {
		return
	}
	_ = c.SetWriteDeadline(time.Now().Add(prazoPrimeiroByte))
	fmt.Fprintf(c, "HTTP/1.1 307 Temporary Redirect\r\nLocation: https://%s%s\r\nConnection: close\r\nContent-Length: 0\r\n\r\n",
		req.Host, req.RequestURI)
}
