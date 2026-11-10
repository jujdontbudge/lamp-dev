package plugin

import (
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

func TestCaseExactRPCAndEmbeddedGrantFields(t *testing.T) {
	for _, raw := range []string{
		`{"jsonrpc":"2.0","JSONRPC":"2.0","id":"x","result":{}}`,
		`{"jsonrpc":"2.0","id":"x","error":{"code":1,"Message":"alias"}}`,
	} {
		var message Message
		if err := decode([]byte(raw), &message); err == nil {
			t.Fatal("RPC case alias accepted", raw)
		}
	}
	var grant Grant
	if err := decode([]byte(`{"id":"opaque","kind":"console","Kind":"ram"}`), &grant); err == nil {
		t.Fatal("embedded grant case alias accepted")
	}
}
func TestNumericValidationAgainstRationalOracle(t *testing.T) {
	random := rand.New(rand.NewSource(731))
	for i := 0; i < 1000; i++ {
		value := json.Number(fmt.Sprintf("%de%+d", random.Int63n(1<<55)-(1<<54), random.Intn(35)-17))
		bound := json.Number(fmt.Sprintf("%de%+d", random.Int63n(1<<55)-(1<<54), random.Intn(35)-17))
		n, nok := new(big.Rat).SetString(value.String())
		limit, lok := new(big.Rat).SetString(bound.String())
		if !nok || !lok {
			t.Fatal("invalid oracle fixture")
		}
		for _, kind := range []string{"number", "integer"} {
			schema := Schema{Type: kind, Maximum: &bound}
			want := n.Cmp(limit) <= 0 && (kind != "integer" || n.IsInt())
			if got := schema.Validate(json.RawMessage(value)) == nil; got != want {
				t.Fatalf("%s %s maximum %s: got %v, rational oracle %v", kind, value, bound, got, want)
			}
		}
	}
	for _, raw := range []string{`{"type":"number","minimum":"1"}`, `{"type":"number","maximum":"2"}`, `{"type":"number","minimum":9007199254740993,"maximum":9007199254740992}`} {
		var schema Schema
		if err := json.Unmarshal([]byte(raw), &schema); err == nil && schema.Validate(json.RawMessage(`9007199254740992`)) == nil {
			t.Fatal("invalid bounds accepted", raw)
		}
	}
}
