package dev

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru"
)

var CACert *tls.Certificate

func SetupOurCert() error {
	dir := mustExpand(supportDir)

	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return err
	}

	key := filepath.Join(dir, "key.pem")
	cert := filepath.Join(dir, "cert.pem")

	tlsCert, err := tls.LoadX509KeyPair(cert, key)
	if err == nil {
		CACert = &tlsCert
		return nil
	}

	err = exec.Command("sh", "-c",
		fmt.Sprintf(
			`openssl req -newkey rsa:2048 -batch -x509 -sha256 -nodes -subj "/C=US/O=Developer Certificate/CN=Puma-dev CA" -keyout '%s' -out '%s' -days 9999`,
			key, cert)).Run()
	if err != nil {
		return err
	}

	return TrustCert(cert)
}

type certCache struct {
	lock  sync.Mutex
	cache *lru.ARCCache
}

func NewCertCache() *certCache {
	cache, err := lru.NewARC(1024)
	if err != nil {
		panic(err)
	}

	return &certCache{
		cache: cache,
	}
}

func (c *certCache) GetCertificate(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.lock.Lock()
	defer c.lock.Unlock()

	name := clientHello.ServerName

	if val, ok := c.cache.Get(name); ok {
		return val.(*tls.Certificate), nil
	}

	cert, err := makeCert(CACert, name)
	if err != nil {
		return nil, err
	}

	c.cache.Add(name, cert)

	return cert, nil
}

func makeCert(
	parent *tls.Certificate,
	name string,
) (*tls.Certificate, error) {

	// start by generating private key
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %v", err)
	}

	// create certificate structure with proper values
	notBefore := time.Now()
	notAfter := notBefore.Add(365 * 24 * time.Hour)
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %v", err)
	}

	cert := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Puma-dev Signed"},
			CommonName:   name,
		},
		NotBefore:   notBefore,
		NotAfter:    notAfter,
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	cert.DNSNames = append(cert.DNSNames, name)

	x509parent, err := x509.ParseCertificate(parent.Certificate[0])
	if err != nil {
		return nil, err
	}

	derBytes, err := x509.CreateCertificate(
		rand.Reader, cert, x509parent, privKey.Public(), parent.PrivateKey)

	if err != nil {
		return nil, fmt.Errorf("could not create certificate: %v", err)
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  privKey,
		Leaf:        cert,
	}

	return tlsCert, nil
}
