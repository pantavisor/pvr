// Copyright 2026 Pantacor Ltd. Licensed under the Apache License, Version 2.0.
package build

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/urfave/cli"
)

// hubCA certifies whatever request it gets, as the Hub's enroll does.
func hubCA(t *testing.T) (func(csrPEM string) string, string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "acme OEM Root CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := x509.ParseCertificate(der)
	issue := func(csrPEM string) string {
		block, _ := pem.Decode([]byte(csrPEM))
		csr, err := x509.ParseCertificateRequest(block.Bytes)
		if err != nil || csr.CheckSignature() != nil {
			t.Fatal("bad csr")
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "acme"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}}
		der, err := x509.CreateCertificate(rand.Reader, leaf, root, csr.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	return issue, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestSigningEnrollKeepsTheKeyHere(t *testing.T) {
	issue, root := hubCA(t)
	var sent []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		sent = append(sent, r.Method+" "+r.URL.Path+" "+string(raw))
		w.Header().Set("Content-Type", "application/json")
		var in map[string]string
		_ = json.Unmarshal(raw, &in)
		out, _ := json.Marshal(map[string]string{"certificate": issue(in["csr"]), "root": root})
		_, _ = w.Write(out)
	}))
	defer server.Close()
	app, out := testApp(t, server.URL)
	app.Commands = []cli.Command{CommandSigning()}
	t.Setenv("PVR_SUDO_TOKEN", "sudo-proof")
	dir := t.TempDir()

	if err := app.Run([]string{"pvr", "signing", "enroll", "acme", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || !strings.HasPrefix(sent[0], "POST /signing/identities/acme/certificates ") {
		t.Fatalf("sent %v", sent)
	}
	if strings.Contains(sent[0], "PRIVATE KEY") || !strings.Contains(sent[0], "CERTIFICATE REQUEST") || !strings.Contains(sent[0], "sudo-proof") {
		t.Fatal("enroll must send a request and the proof, never the key")
	}
	info, err := os.Stat(filepath.Join(dir, "key.pem"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file %v %v", info, err)
	}
	keyPEM, _ := os.ReadFile(filepath.Join(dir, "key.pem"))
	certPEM, _ := os.ReadFile(filepath.Join(dir, "x5c.pem"))
	block, _ := pem.Decode(keyPEM)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	block, _ = pem.Decode(certPEM)
	cert, _ := x509.ParseCertificate(block.Bytes)
	if !key.(*ecdsa.PrivateKey).PublicKey.Equal(cert.PublicKey) {
		t.Fatal("the certificate is not for the key kept here")
	}
	if strings.Contains(out.String(), "PRIVATE KEY") {
		t.Fatal("the key was printed")
	}
	if err := app.Run([]string{"pvr", "signing", "enroll", "acme", "--dir", dir}); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("an existing key was replaced: %v", err)
	}
}

func TestSigningCreateTakesRootCertificatesOnly(t *testing.T) {
	var sent []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&in)
		sent = append(sent, in)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"acme"}`))
	}))
	defer server.Close()
	app, _ := testApp(t, server.URL)
	app.Commands = []cli.Command{CommandSigning()}
	dir := t.TempDir()
	keyFile, rootFile := filepath.Join(dir, "root.key"), filepath.Join(dir, "root.crt")
	_ = os.WriteFile(keyFile, []byte("-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----\n"), 0o600)
	_ = os.WriteFile(rootFile, []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"), 0o644)

	if err := app.Run([]string{"pvr", "signing", "create", "acme", "--oem-root", keyFile}); err == nil || len(sent) != 0 {
		t.Fatalf("a private key file was sent: %v %v", err, sent)
	}
	if err := app.Run([]string{"pvr", "signing", "create", "acme", "--oem-root", rootFile}); err != nil {
		t.Fatal(err)
	}
	if sent[0]["source"] != "own" || !strings.Contains(sent[0]["oem_root"].(string), "CERTIFICATE") {
		t.Fatalf("create sent %v", sent[0])
	}
	if err := app.Run([]string{"pvr", "signing", "create", "acme-hub", "--platform"}); err != nil {
		t.Fatal(err)
	}
	if sent[1]["source"] != "hub" || sent[1]["platform"] != true {
		t.Fatalf("create sent %v", sent[1])
	}
	if err := app.Run([]string{"pvr", "signing", "create", "Bad_Name"}); err == nil {
		t.Fatal("an invalid name was sent")
	}
}
