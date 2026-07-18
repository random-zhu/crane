package model

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// Decimal stores a base-10 decimal without converting financial values to
// float64. Cloud billing APIs return decimal strings and those strings must be
// kept exact until an explicit rounding policy is applied.
type Decimal string

const Zero Decimal = "0"

// NewDecimal validates and canonicalizes a decimal string. Exponent notation
// is deliberately rejected because cloud bill exports use plain decimals and
// accepting exponents makes canonical identifiers harder to audit.
func NewDecimal(value string) (Decimal, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Zero, nil
	}

	if strings.ContainsAny(value, "eE") {
		return "", fmt.Errorf("decimal %q must not use exponent notation", value)
	}

	r, ok := new(big.Rat).SetString(value)
	if !ok {
		return "", fmt.Errorf("invalid decimal %q", value)
	}

	return Decimal(canonicalDecimal(value, r.Sign())), nil
}

func MustDecimal(value string) Decimal {
	d, err := NewDecimal(value)
	if err != nil {
		panic(err)
	}
	return d
}

func (d Decimal) Validate() error {
	_, err := NewDecimal(string(d))
	return err
}

func (d Decimal) Rat() (*big.Rat, error) {
	canonical, err := NewDecimal(string(d))
	if err != nil {
		return nil, err
	}
	r, _ := new(big.Rat).SetString(string(canonical))
	return r, nil
}

func (d Decimal) String() string {
	canonical, err := NewDecimal(string(d))
	if err != nil {
		return string(d)
	}
	return string(canonical)
}

func (d Decimal) IsZero() bool {
	r, err := d.Rat()
	return err == nil && r.Sign() == 0
}

func (d Decimal) Add(other Decimal) (Decimal, error) {
	left, err := d.Rat()
	if err != nil {
		return "", err
	}
	right, err := other.Rat()
	if err != nil {
		return "", err
	}
	return decimalFromRat(new(big.Rat).Add(left, right), 18), nil
}

func (d Decimal) Sub(other Decimal) (Decimal, error) {
	left, err := d.Rat()
	if err != nil {
		return "", err
	}
	right, err := other.Rat()
	if err != nil {
		return "", err
	}
	return decimalFromRat(new(big.Rat).Sub(left, right), 18), nil
}

func (d Decimal) Mul(other Decimal) (Decimal, error) {
	left, err := d.Rat()
	if err != nil {
		return "", err
	}
	right, err := other.Rat()
	if err != nil {
		return "", err
	}
	return decimalFromRat(new(big.Rat).Mul(left, right), 18), nil
}

// Quo divides two decimals and rounds to scale decimal places using
// half-away-from-zero. The scale must be chosen by the caller because billing
// systems use different currency and usage precision.
func (d Decimal) Quo(other Decimal, scale int) (Decimal, error) {
	left, err := d.Rat()
	if err != nil {
		return "", err
	}
	right, err := other.Rat()
	if err != nil {
		return "", err
	}
	if right.Sign() == 0 {
		return "", fmt.Errorf("decimal division by zero")
	}
	if scale < 0 {
		return "", fmt.Errorf("decimal scale must not be negative")
	}
	return decimalFromRat(new(big.Rat).Quo(left, right), scale), nil
}

func (d Decimal) Cmp(other Decimal) (int, error) {
	left, err := d.Rat()
	if err != nil {
		return 0, err
	}
	right, err := other.Rat()
	if err != nil {
		return 0, err
	}
	return left.Cmp(right), nil
}

func (d Decimal) MarshalJSON() ([]byte, error) {
	canonical, err := NewDecimal(string(d))
	if err != nil {
		return nil, err
	}
	return json.Marshal(string(canonical))
}

func (d *Decimal) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*d = Zero
		return nil
	}

	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("decimal must be a JSON string: %w", err)
	}
	parsed, err := NewDecimal(value)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

func canonicalDecimal(value string, sign int) string {
	if sign == 0 {
		return "0"
	}

	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(strings.TrimPrefix(value, "+"), "-")
	parts := strings.SplitN(value, ".", 2)
	integer := strings.TrimLeft(parts[0], "0")
	if integer == "" {
		integer = "0"
	}
	if len(parts) == 1 {
		if negative {
			return "-" + integer
		}
		return integer
	}

	fraction := strings.TrimRight(parts[1], "0")
	result := integer
	if fraction != "" {
		result += "." + fraction
	}
	if negative {
		result = "-" + result
	}
	return result
}

func decimalFromRat(value *big.Rat, scale int) Decimal {
	return Decimal(canonicalDecimal(value.FloatString(scale), value.Sign()))
}
