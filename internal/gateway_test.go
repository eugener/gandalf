package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestHashKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "prefix only", raw: APIKeyPrefix},
		{name: "typical key", raw: "gnd_abc123xyz"},
		{name: "long key", raw: "gnd_" + "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := HashKey(tt.raw)
			h := sha256.Sum256([]byte(tt.raw))
			want := hex.EncodeToString(h[:])
			if got != want {
				t.Errorf("HashKey(%q) = %q, want %q", tt.raw, got, want)
			}
			if len(got) != 64 {
				t.Errorf("HashKey len = %d, want 64", len(got))
			}
		})
	}

	t.Run("deterministic", func(t *testing.T) {
		t.Parallel()
		if HashKey("key") != HashKey("key") {
			t.Error("HashKey is not deterministic")
		}
	})

	t.Run("distinct inputs produce distinct hashes", func(t *testing.T) {
		t.Parallel()
		if HashKey("key1") == HashKey("key2") {
			t.Error("distinct inputs produced same hash")
		}
	})
}

func TestIdentity_Can(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		perms Permission
		check Permission
		want  bool
	}{
		{name: "exact match single", perms: PermUseModels, check: PermUseModels, want: true},
		{name: "superset", perms: PermUseModels | PermManageOwnKeys, check: PermUseModels, want: true},
		{name: "missing", perms: PermManageOwnKeys, check: PermUseModels, want: false},
		{name: "zero perms", perms: 0, check: PermUseModels, want: false},
		{name: "all perms", perms: ^Permission(0), check: PermManageOrgs, want: true},
		{name: "multi-bit check satisfied", perms: PermUseModels | PermManageOwnKeys, check: PermUseModels | PermManageOwnKeys, want: true},
		{name: "multi-bit check partial", perms: PermUseModels, check: PermUseModels | PermManageOwnKeys, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			id := &Identity{Perms: tt.perms}
			if got := id.Can(tt.check); got != tt.want {
				t.Errorf("Can(%v) = %v, want %v (perms=%v)", tt.check, got, tt.want, tt.perms)
			}
		})
	}
}

func TestRolePermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		role  string
		perms []Permission
		lacks []Permission
	}{
		{
			role:  "admin",
			perms: []Permission{PermUseModels, PermManageOwnKeys, PermViewOwnUsage, PermViewAllUsage, PermManageAllKeys, PermManageProviders, PermManageRoutes, PermManageOrgs},
		},
		{
			role:  "member",
			perms: []Permission{PermUseModels, PermManageOwnKeys, PermViewOwnUsage},
			lacks: []Permission{PermViewAllUsage, PermManageAllKeys, PermManageOrgs},
		},
		{
			role:  "viewer",
			perms: []Permission{PermViewOwnUsage, PermViewAllUsage},
			lacks: []Permission{PermUseModels, PermManageOwnKeys},
		},
		{
			role:  "service_account",
			perms: []Permission{PermUseModels},
			lacks: []Permission{PermManageOwnKeys, PermViewOwnUsage, PermManageOrgs},
		},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			t.Parallel()
			p := RolePermissions[tt.role]
			id := &Identity{Perms: p}
			for _, perm := range tt.perms {
				if !id.Can(perm) {
					t.Errorf("role %q: expected Can(%v) = true", tt.role, perm)
				}
			}
			for _, perm := range tt.lacks {
				if id.Can(perm) {
					t.Errorf("role %q: expected Can(%v) = false", tt.role, perm)
				}
			}
		})
	}
}

func TestContextWithRequestID_RequestIDFromContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
	}{
		{name: "non-empty", id: "req-abc-123"},
		{name: "empty string", id: ""},
		{name: "uuid-like", id: "018f1b2c-3d4e-7a5b-8c9d-0e1f2a3b4c5d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := ContextWithRequestID(context.Background(), tt.id)
			got := RequestIDFromContext(ctx)
			if got != tt.id {
				t.Errorf("RequestIDFromContext = %q, want %q", got, tt.id)
			}
		})
	}

	t.Run("missing from context", func(t *testing.T) {
		t.Parallel()
		got := RequestIDFromContext(context.Background())
		if got != "" {
			t.Errorf("RequestIDFromContext on bare ctx = %q, want empty", got)
		}
	})
}

func TestContextWithIdentity_IdentityFromContext(t *testing.T) {
	t.Parallel()

	t.Run("set on bare context", func(t *testing.T) {
		t.Parallel()
		id := &Identity{Subject: "user-1", Role: "admin", Perms: RolePermissions["admin"]}
		ctx := ContextWithIdentity(context.Background(), id)
		got := IdentityFromContext(ctx)
		if got != id {
			t.Errorf("IdentityFromContext = %v, want %v", got, id)
		}
	})

	t.Run("mutates existing meta", func(t *testing.T) {
		t.Parallel()
		// Simulate middleware: requestID set first, identity added later.
		ctx := ContextWithRequestID(context.Background(), "req-xyz")
		id := &Identity{Subject: "svc-1", Role: "service_account"}
		ctx2 := ContextWithIdentity(ctx, id)
		// Same context pointer (no new WithValue).
		if ctx2 != ctx {
			t.Error("ContextWithIdentity should return same ctx when meta already present")
		}
		if got := IdentityFromContext(ctx2); got != id {
			t.Errorf("IdentityFromContext = %v, want %v", got, id)
		}
		// Request ID must still be intact.
		if got := RequestIDFromContext(ctx2); got != "req-xyz" {
			t.Errorf("RequestIDFromContext after ContextWithIdentity = %q, want req-xyz", got)
		}
	})

	t.Run("nil identity", func(t *testing.T) {
		t.Parallel()
		ctx := ContextWithIdentity(context.Background(), nil)
		if got := IdentityFromContext(ctx); got != nil {
			t.Errorf("expected nil identity, got %v", got)
		}
	})

	t.Run("missing from context", func(t *testing.T) {
		t.Parallel()
		if got := IdentityFromContext(context.Background()); got != nil {
			t.Errorf("IdentityFromContext on bare ctx = %v, want nil", got)
		}
	})
}

func TestMetaFromContext(t *testing.T) {
	t.Parallel()

	t.Run("nil on bare context", func(t *testing.T) {
		t.Parallel()
		if m := metaFromContext(context.Background()); m != nil {
			t.Errorf("expected nil, got %v", m)
		}
	})

	t.Run("returns stored meta", func(t *testing.T) {
		t.Parallel()
		ctx := ContextWithRequestID(context.Background(), "r1")
		m := metaFromContext(ctx)
		if m == nil {
			t.Fatal("expected non-nil meta")
		}
		if m.RequestID != "r1" {
			t.Errorf("RequestID = %q, want r1", m.RequestID)
		}
	})

	t.Run("mutation visible through same ctx", func(t *testing.T) {
		t.Parallel()
		ctx := ContextWithRequestID(context.Background(), "r2")
		m := metaFromContext(ctx)
		id := &Identity{Subject: "mutated"}
		m.Identity = id
		if got := IdentityFromContext(ctx); got != id {
			t.Errorf("mutated identity not visible: got %v", got)
		}
	})
}
