//go:build unit

package service

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newSMTPTestCert 生成 127.0.0.1/localhost 的自签证书及其信任池。
func newSMTPTestCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, pool
}

// fakeSMTPServer 是覆盖三种连接形态的最小 SMTP 服务器：
// 隐式 TLS（465 语义）、明文+STARTTLS（587 语义）、纯明文。
type fakeSMTPServer struct {
	listener          net.Listener
	tlsConfig         *tls.Config
	advertiseStartTLS bool

	mu             sync.Mutex
	commands       []string
	messages       []string
	conns          atomic.Int64
	rejectAuth     atomic.Bool
	dropDataReply  atomic.Bool
	rejectGreeting atomic.Bool
	wg             sync.WaitGroup
}

func startFakeSMTPServer(t *testing.T, implicitTLS, advertiseStartTLS bool) (*fakeSMTPServer, int) {
	t.Helper()
	cert, pool := newSMTPTestCert(t)
	prevPool := smtpTestRootCAs
	smtpTestRootCAs = pool
	t.Cleanup(func() { smtpTestRootCAs = prevPool })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &fakeSMTPServer{
		listener:          listener,
		tlsConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		advertiseStartTLS: advertiseStartTLS,
	}
	if implicitTLS {
		srv.listener = tls.NewListener(listener, srv.tlsConfig)
	}
	t.Cleanup(func() {
		_ = srv.listener.Close()
		srv.wg.Wait()
	})

	srv.wg.Add(1)
	go func() {
		defer srv.wg.Done()
		for {
			conn, err := srv.listener.Accept()
			if err != nil {
				return
			}
			srv.conns.Add(1)
			srv.wg.Add(1)
			go func() {
				defer srv.wg.Done()
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				srv.serve(conn, srv.advertiseStartTLS)
			}()
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	return srv, port
}

func (srv *fakeSMTPServer) record(cmd string) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	srv.commands = append(srv.commands, cmd)
}

func (srv *fakeSMTPServer) sawCommand(prefix string) bool {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, cmd := range srv.commands {
		if strings.HasPrefix(strings.ToUpper(cmd), prefix) {
			return true
		}
	}
	return false
}

func (srv *fakeSMTPServer) serve(conn net.Conn, allowStartTLS bool) {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(line string) bool {
		if _, err := writer.WriteString(line + "\r\n"); err != nil {
			return false
		}
		return writer.Flush() == nil
	}
	if srv.rejectGreeting.Load() {
		_ = writeLine("554 5.7.1 service unavailable by policy")
		return
	}
	if !writeLine("220 fake.test ESMTP ready") {
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(line)
		srv.record(cmd)
		upper := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			ok := writeLine("250-fake.test")
			if allowStartTLS {
				ok = ok && writeLine("250-STARTTLS")
			}
			if !(ok && writeLine("250-AUTH PLAIN LOGIN") && writeLine("250 8BITMIME")) {
				return
			}
		case upper == "STARTTLS" && allowStartTLS:
			if !writeLine("220 2.0.0 ready to start TLS") {
				return
			}
			tlsConn := tls.Server(conn, srv.tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			srv.serveUpgraded(tlsConn)
			return
		case strings.HasPrefix(upper, "AUTH"):
			if srv.rejectAuth.Load() {
				_ = writeLine("535 5.7.8 authentication rejected")
				return
			}
			if !writeLine("235 2.7.0 authentication successful") {
				return
			}
		case strings.HasPrefix(upper, "MAIL"), strings.HasPrefix(upper, "RCPT"):
			if !writeLine("250 ok") {
				return
			}
		case upper == "DATA":
			if !writeLine("354 go ahead") {
				return
			}
			var message strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				message.WriteString(dataLine)
			}
			srv.mu.Lock()
			srv.messages = append(srv.messages, message.String())
			srv.mu.Unlock()
			if srv.dropDataReply.Load() {
				return
			}
			if !writeLine("250 message accepted") {
				return
			}
		case upper == "QUIT":
			_ = writeLine("221 bye")
			return
		default:
			if !writeLine("250 ok") {
				return
			}
		}
	}
}

// serveUpgraded 复用命令循环处理 STARTTLS 升级后的会话（升级后不再提供 STARTTLS）。
func (srv *fakeSMTPServer) serveUpgraded(conn net.Conn) {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	// net/smtp 在 StartTLS 成功后会重新发送 EHLO，直接进入命令循环即可。
	srv.serveCommands(reader, writer)
}

func (srv *fakeSMTPServer) serveCommands(reader *bufio.Reader, writer *bufio.Writer) {
	writeLine := func(line string) bool {
		if _, err := writer.WriteString(line + "\r\n"); err != nil {
			return false
		}
		return writer.Flush() == nil
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimSpace(line)
		srv.record(cmd)
		upper := strings.ToUpper(cmd)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			if !(writeLine("250-fake.test") && writeLine("250-AUTH PLAIN LOGIN") && writeLine("250 8BITMIME")) {
				return
			}
		case strings.HasPrefix(upper, "AUTH"):
			if srv.rejectAuth.Load() {
				_ = writeLine("535 5.7.8 authentication rejected")
				return
			}
			if !writeLine("235 2.7.0 authentication successful") {
				return
			}
		case strings.HasPrefix(upper, "MAIL"), strings.HasPrefix(upper, "RCPT"):
			if !writeLine("250 ok") {
				return
			}
		case upper == "DATA":
			if !writeLine("354 go ahead") {
				return
			}
			var message strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				message.WriteString(dataLine)
			}
			srv.mu.Lock()
			srv.messages = append(srv.messages, message.String())
			srv.mu.Unlock()
			if srv.dropDataReply.Load() {
				return
			}
			if !writeLine("250 message accepted") {
				return
			}
		case upper == "QUIT":
			_ = writeLine("221 bye")
			return
		default:
			if !writeLine("250 ok") {
				return
			}
		}
	}
}

func smtpTestConfig(port int, useTLS bool) *SMTPConfig {
	return &SMTPConfig{
		Host:     "127.0.0.1",
		Port:     port,
		Username: "user",
		Password: "pass",
		From:     "noreply@example.com",
		FromName: "Test",
		UseTLS:   useTLS,
	}
}

// 465 语义：UseTLS=true + 隐式 TLS 服务器，原有路径保持可用。
func TestSMTPConnectionImplicitTLS(t *testing.T) {
	srv, port := startFakeSMTPServer(t, true, false)
	svc := &EmailService{}

	if err := svc.TestSMTPConnectionWithConfig(smtpTestConfig(port, true)); err != nil {
		t.Fatalf("expected implicit TLS connection to succeed, got: %v", err)
	}
	if !srv.sawCommand("EHLO") {
		t.Fatal("expected server to receive EHLO")
	}
}

// 587 语义（#1470/#1488 核心场景）：UseTLS=true + 明文问候的 STARTTLS 服务器，
// 隐式 TLS 失败后必须自动降级为强制 STARTTLS 并成功。
func TestSMTPConnectionStartTLSFallbackWhenTLSEnabled(t *testing.T) {
	srv, port := startFakeSMTPServer(t, false, true)
	svc := &EmailService{}

	if err := svc.TestSMTPConnectionWithConfig(smtpTestConfig(port, true)); err != nil {
		t.Fatalf("expected STARTTLS fallback to succeed, got: %v", err)
	}
	if !srv.sawCommand("STARTTLS") {
		t.Fatal("expected server to receive STARTTLS command")
	}
	if got := srv.conns.Load(); got < 2 {
		t.Fatalf("expected implicit TLS attempt before STARTTLS fallback (>=2 connections), got %d", got)
	}
}

// UseTLS=true 但服务器不支持 STARTTLS：必须报错，且绝不能把凭据发到明文连接上。
func TestSMTPConnectionMandatoryStartTLSRefusesPlaintext(t *testing.T) {
	srv, port := startFakeSMTPServer(t, false, false)
	svc := &EmailService{}

	err := svc.TestSMTPConnectionWithConfig(smtpTestConfig(port, true))
	if err == nil {
		t.Fatal("expected error when server does not support STARTTLS")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("expected STARTTLS-related error, got: %v", err)
	}
	if srv.sawCommand("AUTH") {
		t.Fatal("credentials must not be sent over plaintext when TLS is required")
	}
}

// UseTLS=false + 服务器支持 STARTTLS：测试连接与发送路径一致，机会式升级后认证成功。
// 这是 #1488 评论"测试连接不成功，发送测试邮件实际上能发"的回归用例。
func TestSMTPConnectionOpportunisticStartTLSWhenTLSDisabled(t *testing.T) {
	srv, port := startFakeSMTPServer(t, false, true)
	svc := &EmailService{}

	if err := svc.TestSMTPConnectionWithConfig(smtpTestConfig(port, false)); err != nil {
		t.Fatalf("expected opportunistic STARTTLS test connection to succeed, got: %v", err)
	}
	if !srv.sawCommand("STARTTLS") {
		t.Fatal("expected test connection to upgrade via STARTTLS like the send path")
	}
}

// UseTLS=false + 服务器不支持 STARTTLS：保持明文直连（既有行为不回归）。
func TestSMTPConnectionPlainWhenNoStartTLS(t *testing.T) {
	srv, port := startFakeSMTPServer(t, false, false)
	svc := &EmailService{}

	if err := svc.TestSMTPConnectionWithConfig(smtpTestConfig(port, false)); err != nil {
		t.Fatalf("expected plain connection to succeed, got: %v", err)
	}
	if srv.sawCommand("STARTTLS") {
		t.Fatal("did not expect STARTTLS command when server does not advertise it")
	}
}

// 发送路径全流程：UseTLS=true + STARTTLS 服务器（587 语义）完整走完 MAIL/RCPT/DATA。
func TestSendEmailWithConfigStartTLSFallback(t *testing.T) {
	srv, port := startFakeSMTPServer(t, false, true)
	svc := &EmailService{}

	err := svc.SendEmailWithConfig(smtpTestConfig(port, true), "rcpt@example.com", "subject", "<p>body</p>")
	if err != nil {
		t.Fatalf("expected send via STARTTLS fallback to succeed, got: %v", err)
	}
	if !srv.sawCommand("STARTTLS") {
		t.Fatal("expected send path to upgrade via STARTTLS")
	}
	if !srv.sawCommand("DATA") {
		t.Fatal("expected send path to reach DATA")
	}
}

// 发送路径全流程：UseTLS=true + 隐式 TLS 服务器（465 语义）保持既有行为。
func TestSendEmailWithConfigImplicitTLS(t *testing.T) {
	srv, port := startFakeSMTPServer(t, true, false)
	svc := &EmailService{}

	err := svc.SendEmailWithConfig(smtpTestConfig(port, true), "rcpt@example.com", "subject", "<p>body</p>")
	if err != nil {
		t.Fatalf("expected send via implicit TLS to succeed, got: %v", err)
	}
	if !srv.sawCommand("DATA") {
		t.Fatal("expected send path to reach DATA")
	}
}

// Only documented encrypted Resend submission ports gain a backup; arbitrary
// hosts, custom ports and existing optional/plain TLS modes remain unchanged.
func TestSMTPResendEndpointPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config SMTPConfig
		want   []smtpEndpoint
	}{
		{"implicit primary", SMTPConfig{Host: "smtp.resend.com", Port: 2465, UseTLS: true}, []smtpEndpoint{{port: 2465}, {port: 2587, startTLS: true}}},
		{"blocked standard implicit", SMTPConfig{Host: "SMTP.RESEND.COM.", Port: 465, UseTLS: true}, []smtpEndpoint{{port: 465}, {port: 2587, startTLS: true}}},
		{"STARTTLS primary", SMTPConfig{Host: "smtp.resend.com", Port: 2587, UseTLS: true}, []smtpEndpoint{{port: 2587, startTLS: true}, {port: 2465}}},
		{"standard STARTTLS", SMTPConfig{Host: "smtp.resend.com", Port: 587, UseTLS: true}, []smtpEndpoint{{port: 587, startTLS: true}, {port: 2465}}},
		{"other host", SMTPConfig{Host: "mail.example.com", Port: 2465, UseTLS: true}, []smtpEndpoint{{port: 2465}}},
		{"host suffix", SMTPConfig{Host: "smtp.resend.com.example", Port: 2465, UseTLS: true}, []smtpEndpoint{{port: 2465}}},
		{"custom port", SMTPConfig{Host: "smtp.resend.com", Port: 12345, UseTLS: true}, []smtpEndpoint{{port: 12345}}},
		{"optional TLS", SMTPConfig{Host: "smtp.resend.com", Port: 2465}, []smtpEndpoint{{port: 2465}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := smtpEndpoints(&tc.config); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("endpoints: got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func smtpTestMessage(t *testing.T, config *SMTPConfig) smtpMessage {
	t.Helper()
	message, err := buildSMTPMessage(config, "rcpt@example.com", "fallback subject", "<p>one body</p>")
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func smtpMessageCount(srv *fakeSMTPServer) int {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return len(srv.messages)
}

// Exercise the actual prepared-message send operation against real sockets:
// the primary TCP endpoint refuses connections, then the backup upgrades using
// mandatory STARTTLS and receives exactly one DATA transaction.
func TestSMTPSendFallbackBeforeSubmission(t *testing.T) {
	unavailable, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	primaryPort := unavailable.Addr().(*net.TCPAddr).Port
	defer unavailable.Close()
	backup, backupPort := startFakeSMTPServer(t, false, true)
	// Keep the primary reservation until the backup is bound, avoiding reuse.
	if err := unavailable.Close(); err != nil {
		t.Fatal(err)
	}
	config := smtpTestConfig(primaryPort, true)
	err = (&EmailService{}).sendSMTPMessage(config, smtpTestMessage(t, config), []smtpEndpoint{{port: primaryPort}, {port: backupPort, startTLS: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !backup.sawCommand("STARTTLS") {
		t.Fatal("fallback must negotiate STARTTLS")
	}
	if got := smtpMessageCount(backup); got != 1 {
		t.Fatalf("got %d messages, want exactly one", got)
	}
}

func TestSMTPSendDoesNotFallbackAfterSession(t *testing.T) {
	for _, scenario := range []string{"primary success", "AUTH rejected", "DATA acknowledgement lost"} {
		t.Run(scenario, func(t *testing.T) {
			primary, primaryPort := startFakeSMTPServer(t, true, false)
			primaryRoots := smtpTestRootCAs.Clone()
			backup, backupPort := startFakeSMTPServer(t, false, true)
			// Both local test CAs remain trusted; production uses system roots.
			leaf, err := x509.ParseCertificate(backup.tlsConfig.Certificates[0].Certificate[0])
			if err != nil {
				t.Fatal(err)
			}
			primaryRoots.AddCert(leaf)
			smtpTestRootCAs = primaryRoots
			if scenario == "AUTH rejected" {
				primary.rejectAuth.Store(true)
			}
			if scenario == "DATA acknowledgement lost" {
				primary.dropDataReply.Store(true)
			}
			config := smtpTestConfig(primaryPort, true)
			err = (&EmailService{}).sendSMTPMessage(config, smtpTestMessage(t, config), []smtpEndpoint{{port: primaryPort}, {port: backupPort, startTLS: true}})
			if (err == nil) != (scenario == "primary success") {
				t.Fatalf("unexpected send result: %v", err)
			}
			if got := backup.conns.Load(); got != 0 {
				t.Fatalf("backup got %d connections after primary session", got)
			}
			wantMessages := 1
			if scenario == "AUTH rejected" {
				wantMessages = 0
			}
			if got := smtpMessageCount(primary); got != wantMessages {
				t.Fatalf("got %d primary messages, want %d", got, wantMessages)
			}
		})
	}
}

func TestSMTPFallbackRequiresEncryptedBackup(t *testing.T) {
	primary, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := primary.Addr().(*net.TCPAddr).Port
	defer primary.Close()
	backup, backupPort := startFakeSMTPServer(t, false, false)
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	config := smtpTestConfig(port, true)
	err = (&EmailService{}).sendSMTPMessage(config, smtpTestMessage(t, config), []smtpEndpoint{{port: port}, {port: backupPort, startTLS: true}})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("expected mandatory STARTTLS rejection, got %v", err)
	}
	if backup.sawCommand("AUTH") || backup.sawCommand("DATA") {
		t.Fatal("plaintext backup received credentials or message")
	}
}

func TestSMTPFallbackDoesNotHideCertificateOrGreetingRejection(t *testing.T) {
	for _, scenario := range []string{"untrusted certificate", "SMTP greeting rejected"} {
		t.Run(scenario, func(t *testing.T) {
			primary, primaryPort := startFakeSMTPServer(t, true, false)
			primaryRoots := smtpTestRootCAs.Clone()
			backup, backupPort := startFakeSMTPServer(t, false, true)
			if scenario == "SMTP greeting rejected" {
				smtpTestRootCAs = primaryRoots
				primary.rejectGreeting.Store(true)
			}
			config := smtpTestConfig(primaryPort, true)
			err := (&EmailService{}).sendSMTPMessage(config, smtpTestMessage(t, config), []smtpEndpoint{{port: primaryPort}, {port: backupPort, startTLS: true}})
			if err == nil {
				t.Fatal("expected primary policy/certificate error")
			}
			if got := backup.conns.Load(); got != 0 {
				t.Fatalf("backup got %d connections after non-network primary rejection", got)
			}
		})
	}
}
