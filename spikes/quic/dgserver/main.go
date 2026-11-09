// Datagram spike: can Network.framework take QUIC datagrams (RFC 9221)
// from quic-go? A server that, on each connection, sends 2000 numbered
// 1100-byte datagrams (about a Tailscale MTU's worth each) in bursts, as
// frames would go, and prints the fingerprint to pin.
//
//   go run ./spikes/quic/dgserver 127.0.0.1:7499
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/quic-go/quic-go"
)

func main() {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	sum := sha256.Sum256(der)
	fmt.Println("fingerprint", hex.EncodeToString(sum[:]))
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	ln, err := quic.ListenAddr(os.Args[1], &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"hmk-media/1"}},
		&quic.Config{EnableDatagrams: true, MaxIdleTimeout: 15 * time.Second})
	if err != nil {
		panic(err)
	}
	for {
		qc, err := ln.Accept(context.Background())
		if err != nil {
			panic(err)
		}
		go func() {
			st := qc.ConnectionState()
			fmt.Println("connection: datagrams", st.SupportsDatagrams)
			time.Sleep(300 * time.Millisecond)
			b := make([]byte, 1100)
			sent := 0
			for i := 0; i < 2000; i++ {
				binary.BigEndian.PutUint32(b, uint32(i))
				binary.BigEndian.PutUint64(b[4:], uint64(time.Now().UnixMicro()))
				if err := qc.SendDatagram(b); err != nil {
					fmt.Println("send", i, err)
					break
				}
				sent++
				if i%50 == 49 {
					time.Sleep(16 * time.Millisecond) // a frame's worth, then the next
				}
			}
			fmt.Println("sent", sent)
			time.Sleep(2 * time.Second)
			qc.CloseWithError(0, "")
		}()
	}
}
