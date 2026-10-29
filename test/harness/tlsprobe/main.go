/*
Copyright 2026 maeshinshin.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Command tlsprobe is a static TLS/plaintext probe for the hardware tests: it
// prints the certificate a server presents (SANs, serial, validity), whether it verifies against
// a CA file, whether plain HTTP is answered, and with -watch how long until the served serial
// changes. It is copied into the tools pod because the pod has no openssl or curl.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"net"
	"os"
	"time"
)

func main() {
	addr := flag.String("addr", "", "host:port")
	caFile := flag.String("ca", "", "PEM CA file to verify against")
	sni := flag.String("sni", "", "server name to send and verify")
	plain := flag.Bool("plain", false, "send a plain HTTP request instead of a handshake")
	watch := flag.Duration("watch", 0, "poll every 500ms for this long and print each serial change")
	flag.Parse()

	if *plain {
		os.Exit(plainProbe(*addr))
	}

	cfg := &tls.Config{ServerName: *sni, InsecureSkipVerify: true} //nolint:gosec // inspection tool; verification is reported separately

	var pool *x509.CertPool

	if *caFile != "" {
		pem, err := os.ReadFile(*caFile)
		if err != nil {
			fmt.Println("read ca:", err)
			os.Exit(2)
		}

		pool = x509.NewCertPool()
		pool.AppendCertsFromPEM(pem)
	}

	if *watch == 0 {
		os.Exit(once(*addr, cfg, pool, true))
	}

	last := ""
	start := time.Now()

	for time.Since(start) < *watch {
		serial := serialOf(*addr, cfg)
		if serial != last {
			fmt.Printf("t+%6.1fs serial=%s\n", time.Since(start).Seconds(), serial)
			last = serial
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func serialOf(addr string, cfg *tls.Config) string {
	d := net.Dialer{Timeout: 3 * time.Second}

	c, err := tls.DialWithDialer(&d, "tcp", addr, cfg)
	if err != nil {
		return "error: " + err.Error()
	}
	defer c.Close()

	return c.ConnectionState().PeerCertificates[0].SerialNumber.Text(16)
}

func once(addr string, cfg *tls.Config, pool *x509.CertPool, verbose bool) int {
	d := net.Dialer{Timeout: 5 * time.Second}

	c, err := tls.DialWithDialer(&d, "tcp", addr, cfg)
	if err != nil {
		fmt.Println("handshake failed:", err)
		return 1
	}
	defer c.Close()

	st := c.ConnectionState()
	leaf := st.PeerCertificates[0]

	if verbose {
		fmt.Printf("tls=%x subject=%s issuer=%s\nserial=%s notBefore=%s notAfter=%s\ndns=%v ips=%v\n",
			st.Version, leaf.Subject, leaf.Issuer, leaf.SerialNumber.Text(16),
			leaf.NotBefore.UTC().Format(time.RFC3339), leaf.NotAfter.UTC().Format(time.RFC3339),
			leaf.DNSNames, leaf.IPAddresses)
	}

	if pool != nil {
		inter := x509.NewCertPool()
		for _, p := range st.PeerCertificates[1:] {
			inter.AddCert(p)
		}

		_, verr := leaf.Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter, DNSName: cfg.ServerName})
		fmt.Println("verify against ca:", map[bool]string{true: "OK", false: fmt.Sprint("FAILED: ", verr)}[verr == nil])
	}

	return 0
}

func plainProbe(addr string) int {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Println("plaintext: connect failed:", err)
		return 1
	}
	defer c.Close()

	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = c.Write([]byte("GET /auth/config HTTP/1.0\r\n\r\n"))

	buf := make([]byte, 200)
	n, err := c.Read(buf)
	fmt.Printf("plaintext: read %d bytes err=%v first=%q\n", n, err, string(buf[:n]))

	return 0
}
