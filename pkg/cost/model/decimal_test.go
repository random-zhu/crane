package model

import (
	"encoding/json"
	"testing"
)

func TestDecimalCanonicalAndMath(t *testing.T) {
	tests := map[string]string{
		"00012.3400": "12.34",
		"-0.000":     "0",
		".50":        "0.5",
		"-001.20":    "-1.2",
	}
	for input, want := range tests {
		got, err := NewDecimal(input)
		if err != nil {
			t.Fatalf("NewDecimal(%q): %v", input, err)
		}
		if got.String() != want {
			t.Errorf("NewDecimal(%q) = %q, want %q", input, got, want)
		}
	}

	left := MustDecimal("10.25")
	right := MustDecimal("2.5")
	if got, _ := left.Add(right); got != "12.75" {
		t.Fatalf("add = %s", got)
	}
	if got, _ := left.Sub(right); got != "7.75" {
		t.Fatalf("sub = %s", got)
	}
	if got, _ := left.Mul(right); got != "25.625" {
		t.Fatalf("mul = %s", got)
	}
	if got, _ := left.Quo(right, 4); got != "4.1" {
		t.Fatalf("quo = %s", got)
	}
}

func TestDecimalJSONRequiresString(t *testing.T) {
	type payload struct {
		Value Decimal `json:"value"`
	}
	data, err := json.Marshal(payload{Value: MustDecimal("12.3400")})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"value":"12.34"}` {
		t.Fatalf("unexpected json %s", data)
	}

	var p payload
	if err := json.Unmarshal([]byte(`{"value":12.34}`), &p); err == nil {
		t.Fatal("expected numeric JSON value to be rejected")
	}
}

func TestDecimalRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"NaN", "Inf", "1e3", "1.2.3"} {
		if _, err := NewDecimal(value); err == nil {
			t.Errorf("expected %q to fail", value)
		}
	}
}
