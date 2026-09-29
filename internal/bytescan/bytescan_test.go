package bytescan

import (
	"bytes"
	"context"
	"reflect"
	"testing"
)

func TestIndexAll(t *testing.T) {
	got := IndexAll([]byte("aXXbXXcXX"), []byte("XX"))
	want := []int{1, 4, 7}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("IndexAll = %v, want %v", got, want)
	}
	if got := IndexAll([]byte("abc"), []byte("z")); got != nil {
		t.Errorf("IndexAll(no match) = %v, want nil", got)
	}
}

// TestScan_AcrossBoundary ensures a needle that straddles the internal chunk
// boundary is found exactly once, with the correct absolute offset.
func TestScan_AcrossBoundary(t *testing.T) {
	needle := []byte("NEEDLE")
	positions := []int{10, chunkSize - 3, chunkSize + 50} // middle, straddling, next chunk

	total := chunkSize + 200
	data := make([]byte, total)
	for i := range data {
		data[i] = 'a'
	}
	for _, p := range positions {
		copy(data[p:], needle)
	}

	var found []int
	err := Scan(context.Background(), bytes.NewReader(data), 16, func(win []byte, base int64, safe int) {
		for _, p := range IndexAll(win[:safe], needle) {
			found = append(found, int(base)+p)
		}
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !reflect.DeepEqual(found, positions) {
		t.Errorf("found offsets = %v, want %v", found, positions)
	}
}
