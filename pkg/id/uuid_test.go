package id

import (
"testing"

"github.com/google/uuid"
)

func TestNewID_Unique(t *testing.T) {
seen := make(map[string]struct{}, 1000)
for i := 0; i < 1000; i++ {
id := NewID()
if _, dup := seen[id]; dup {
t.Fatalf("NewID 生成重复 ID: %s", id)
}
seen[id] = struct{}{}
}
}

func TestNewID_IsValidUUIDv7(t *testing.T) {
id := NewID()
parsed, err := uuid.Parse(id)
if err != nil {
t.Fatalf("NewID 返回非法 UUID %q: %v", id, err)
}
if v := parsed.Version(); v != 7 {
t.Fatalf("NewID 应返回 UUIDv7，实际版本 %d", v)
}
}
