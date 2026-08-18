package main

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestReadIdempotencyKey(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   string
	}{
		{
			name:   "missing",
			header: http.Header{},
		},
		{
			name:   "repeated header lines",
			header: http.Header{"Idempotency-Key": []string{"one", "two"}},
		},
		{
			name:   "zero length",
			header: http.Header{"Idempotency-Key": []string{""}},
		},
		{
			name:   "256 bytes",
			header: http.Header{"Idempotency-Key": []string{strings.Repeat("a", 256)}},
		},
		{
			name:   "whitespace",
			header: http.Header{"Idempotency-Key": []string{"key value"}},
		},
		{
			name:   "unicode",
			header: http.Header{"Idempotency-Key": []string{"café"}},
		},
		{
			name:   "control byte",
			header: http.Header{"Idempotency-Key": []string{"key\nvalue"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := readIdempotencyKey(tt.header); err == nil {
				t.Fatalf("readIdempotencyKey() = %q, want error", got)
			}
		})
	}
}

func TestReadIdempotencyKeyAcceptsVisibleASCII(t *testing.T) {
	for _, want := range []string{"ABC", "abc", "visible-~!"} {
		t.Run(want, func(t *testing.T) {
			got, err := readIdempotencyKey(http.Header{"Idempotency-Key": []string{want}})
			if err != nil {
				t.Fatalf("readIdempotencyKey() error = %v", err)
			}
			if got != want {
				t.Fatalf("readIdempotencyKey() = %q, want %q", got, want)
			}
		})
	}

	upper, err := readIdempotencyKey(http.Header{"Idempotency-Key": []string{"ABC"}})
	if err != nil {
		t.Fatalf("readIdempotencyKey(ABC) error = %v", err)
	}
	lower, err := readIdempotencyKey(http.Header{"Idempotency-Key": []string{"abc"}})
	if err != nil {
		t.Fatalf("readIdempotencyKey(abc) error = %v", err)
	}
	if upper == lower {
		t.Fatalf("readIdempotencyKey() collapsed case-distinct keys: %q", upper)
	}
}

func TestNormalizeAmount(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "integer", input: "100", want: "100.00"},
		{name: "one fractional digit", input: "100.0", want: "100.00"},
		{name: "two fractional digits", input: "100.00", want: "100.00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeAmount(json.Number(tt.input))
			if err != nil {
				t.Fatalf("normalizeAmount() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeAmount() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeAmountRejectsInvalidValues(t *testing.T) {
	for _, input := range []string{"100.000", "1e2", "0", "-1", "0.001", "1000000000000000000.00"} {
		t.Run(input, func(t *testing.T) {
			if _, err := normalizeAmount(json.Number(input)); err == nil {
				t.Fatalf("normalizeAmount(%q) error = nil, want error", input)
			}
		})
	}
}

func TestDecodeTransferRequestCanonicalizesEquivalentAmounts(t *testing.T) {
	for _, amount := range []string{"100", "100.0", "100.00"} {
		t.Run(amount, func(t *testing.T) {
			request, err := decodeTransferRequest(strings.NewReader(`{"source_wallet_id":1,"destination_wallet_id":2,"amount":` + amount + `}`))
			if err != nil {
				t.Fatalf("decodeTransferRequest() error = %v", err)
			}
			want := transferRequest{SourceWalletID: 1, DestinationWalletID: 2, Amount: "100.00"}
			if request != want {
				t.Fatalf("decodeTransferRequest() = %#v, want %#v", request, want)
			}
		})
	}
}

func TestDecodeTransferRequestRejectsInvalidJSON(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "numeric string", body: `{"source_wallet_id":1,"destination_wallet_id":2,"amount":"100"}`},
		{name: "exponent amount", body: `{"source_wallet_id":1,"destination_wallet_id":2,"amount":1e2}`},
		{name: "zero amount", body: `{"source_wallet_id":1,"destination_wallet_id":2,"amount":0}`},
		{name: "negative amount", body: `{"source_wallet_id":1,"destination_wallet_id":2,"amount":-1}`},
		{name: "too many fractional digits", body: `{"source_wallet_id":1,"destination_wallet_id":2,"amount":0.001}`},
		{name: "missing source ID", body: `{"destination_wallet_id":2,"amount":100}`},
		{name: "missing destination ID", body: `{"source_wallet_id":1,"amount":100}`},
		{name: "zero source ID", body: `{"source_wallet_id":0,"destination_wallet_id":2,"amount":100}`},
		{name: "zero destination ID", body: `{"source_wallet_id":1,"destination_wallet_id":0,"amount":100}`},
		{name: "equal IDs", body: `{"source_wallet_id":1,"destination_wallet_id":1,"amount":100}`},
		{name: "unknown field", body: `{"source_wallet_id":1,"destination_wallet_id":2,"amount":100,"extra":true}`},
		{name: "duplicate field", body: `{"source_wallet_id":1,"source_wallet_id":2,"destination_wallet_id":3,"amount":100}`},
		{name: "second JSON object", body: `{"source_wallet_id":1,"destination_wallet_id":2,"amount":100}{}`},
		{name: "trailing text", body: `{"source_wallet_id":1,"destination_wallet_id":2,"amount":100} trailing`},
		{name: "above NUMERIC maximum", body: `{"source_wallet_id":1,"destination_wallet_id":2,"amount":1000000000000000000.00}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeTransferRequest(strings.NewReader(tt.body)); err == nil {
				t.Fatal("decodeTransferRequest() error = nil, want error")
			}
		})
	}
}

func TestRequestFingerprintCanonicalizesEquivalentAmounts(t *testing.T) {
	requests := make([]transferRequest, 0, 3)
	for _, amount := range []string{"100", "100.0", "100.00"} {
		request, err := decodeTransferRequest(strings.NewReader(`{"source_wallet_id":1,"destination_wallet_id":2,"amount":` + amount + `}`))
		if err != nil {
			t.Fatalf("decodeTransferRequest(%s) error = %v", amount, err)
		}
		requests = append(requests, request)
	}

	want, err := requestFingerprint(requests[0])
	if err != nil {
		t.Fatalf("requestFingerprint() error = %v", err)
	}
	for _, request := range requests[1:] {
		got, err := requestFingerprint(request)
		if err != nil {
			t.Fatalf("requestFingerprint() error = %v", err)
		}
		if got != want {
			t.Fatalf("requestFingerprint(%#v) = %s, want %s", request, hex.EncodeToString(got[:]), hex.EncodeToString(want[:]))
		}
	}
}

func TestRequestFingerprintChangesWhenRequestChanges(t *testing.T) {
	base := transferRequest{SourceWalletID: 1, DestinationWalletID: 2, Amount: "100.00"}
	tests := []struct {
		name    string
		request transferRequest
	}{
		{name: "source", request: transferRequest{SourceWalletID: 3, DestinationWalletID: 2, Amount: "100.00"}},
		{name: "destination", request: transferRequest{SourceWalletID: 1, DestinationWalletID: 3, Amount: "100.00"}},
		{name: "amount", request: transferRequest{SourceWalletID: 1, DestinationWalletID: 2, Amount: "100.01"}},
	}

	want, err := requestFingerprint(base)
	if err != nil {
		t.Fatalf("requestFingerprint() error = %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := requestFingerprint(tt.request)
			if err != nil {
				t.Fatalf("requestFingerprint() error = %v", err)
			}
			if got == want {
				t.Fatalf("requestFingerprint(%#v) = %s, want a different fingerprint", tt.request, hex.EncodeToString(got[:]))
			}
		})
	}
}
