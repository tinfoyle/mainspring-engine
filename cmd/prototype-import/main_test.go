package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfigRequiresDedicatedSecretsAndStrictBooleans(t *testing.T) {
	values := map[string]string{
		"SPYGLASS_PROTOTYPE_IMPORT_GLOBAL_DATABASE_URL":                "postgres://global",
		"SPYGLASS_PROTOTYPE_IMPORT_CELL_DATABASE_URL":                  "postgres://cell",
		"SPYGLASS_PROTOTYPE_IMPORT_OBJECT_ENDPOINT":                    "minio:9000",
		"SPYGLASS_PROTOTYPE_IMPORT_OBJECT_BUCKET":                      "spyglass",
		"SPYGLASS_PROTOTYPE_IMPORT_OBJECT_ACCESS_KEY":                  "access",
		"SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SECRET_KEY":                  "secret",
		"SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SECURE":                      "false",
		"SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SSE":                         "true",
		"SPYGLASS_PROTOTYPE_IMPORT_CELL_ID":                            "cell-us-east-01",
		"SPYGLASS_PROTOTYPE_IMPORT_GLOBAL_ERASURE_CHECKPOINT_SEQUENCE": "0",
		"SPYGLASS_PROTOTYPE_IMPORT_GLOBAL_ERASURE_CHECKPOINT_ROOT":     "0000000000000000000000000000000000000000000000000000000000000000",
		"SPYGLASS_PROTOTYPE_IMPORT_CELL_ERASURE_CHECKPOINT_SEQUENCE":   "0",
		"SPYGLASS_PROTOTYPE_IMPORT_CELL_ERASURE_CHECKPOINT_ROOT":       "0000000000000000000000000000000000000000000000000000000000000000",
	}
	getenv := func(name string) string { return values[name] }
	value, err := configFromEnvironment(getenv)
	if err != nil || value.objectSecure || !value.objectSSE || value.objectSecretKey != "secret" {
		t.Fatalf("config=%+v err=%v", value, err)
	}
	values["SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SECURE"] = "1"
	if _, err := configFromEnvironment(getenv); err == nil {
		t.Fatal("non-canonical boolean accepted")
	}
	values["SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SECURE"] = "false"
	values["SPYGLASS_PROTOTYPE_IMPORT_CELL_ID"] = "cell-us-west-01/other"
	if _, err := configFromEnvironment(getenv); err == nil {
		t.Fatal("invalid destination cell identity accepted")
	}
	values["SPYGLASS_PROTOTYPE_IMPORT_CELL_ID"] = "cell-us-east-01"
	values["SPYGLASS_PROTOTYPE_IMPORT_CELL_ERASURE_CHECKPOINT_SEQUENCE"] = "1"
	if _, err := configFromEnvironment(getenv); err == nil {
		t.Fatal("non-zero restore sequence with the zero root accepted")
	}
	values["SPYGLASS_PROTOTYPE_IMPORT_CELL_ERASURE_CHECKPOINT_SEQUENCE"] = "0"
	delete(values, "SPYGLASS_PROTOTYPE_IMPORT_OBJECT_SECRET_KEY")
	if _, err := configFromEnvironment(getenv); err == nil {
		t.Fatal("missing object secret accepted")
	}
}

func TestCertificateIsPrivateAndNeverOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence", "reconciliation.json")
	value := reconciliationCertificate{Version: "v1", ReconciliationSHA256: "digest", CompletedAt: time.Now().UTC(), OwnerReviewRequired: true}
	if err := writeCertificate(path, value); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode(), err)
	}
	if err := writeCertificate(path, value); err == nil {
		t.Fatal("existing certificate was overwritten")
	}
}
