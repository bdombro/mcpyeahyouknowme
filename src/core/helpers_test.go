package core

import (
	"testing"
)

func TestIntArg_int(t *testing.T) {
	args := map[string]interface{}{"n": 42}
	if got := IntArg(args, "n", 0); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}

func TestIntArg_float64(t *testing.T) {
	args := map[string]interface{}{"n": float64(7)}
	if got := IntArg(args, "n", 0); got != 7 {
		t.Errorf("expected 7, got %d", got)
	}
}

func TestIntArg_missing(t *testing.T) {
	args := map[string]interface{}{}
	if got := IntArg(args, "n", 99); got != 99 {
		t.Errorf("expected default 99, got %d", got)
	}
}

func TestJsonResult_valid(t *testing.T) {
	result, err := JsonResult(map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("JsonResult: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestJsonResult_unmarshalable(t *testing.T) {
	// JsonResult converts marshal errors into a tool error result (never returns err).
	ch := make(chan int)
	result, err := JsonResult(ch)
	if err != nil {
		t.Fatalf("unexpected error: JsonResult should not return err, got %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for unmarshalable value")
	}
}
