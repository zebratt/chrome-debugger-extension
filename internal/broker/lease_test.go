package broker

import (
	"errors"
	"testing"
	"time"
)

func TestTabLeaseBlocksOtherWriterUntilRelease(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	store := NewLeaseStore(30*time.Second, func() time.Time { return now })
	first, err := store.Claim("profile-a", 7, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == "" || !first.ExpiresAt.Equal(now.Add(30*time.Second)) {
		t.Fatalf("invalid lease: %+v", first)
	}
	if _, err := store.Claim("profile-a", 7, ""); !errors.Is(err, ErrTabBusy) {
		t.Fatalf("second writer got %v, want ErrTabBusy", err)
	}
	if err := store.Check("profile-a", 7, first.Token); err != nil {
		t.Fatal(err)
	}
	if err := store.Release("profile-a", 7, first.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("profile-a", 7, ""); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
}

func TestTabLeaseExpiresAndCannotBeReused(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	store := NewLeaseStore(30*time.Second, func() time.Time { return now })
	first, err := store.Claim("profile-a", 7, "")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Second)
	if err := store.Check("profile-a", 7, first.Token); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("expired lease got %v", err)
	}
	second, err := store.Claim("profile-a", 7, "")
	if err != nil || second.Token == first.Token {
		t.Fatalf("new claim %+v, %v", second, err)
	}
}

func TestTabLeaseRenewalAndProfileRevocation(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	store := NewLeaseStore(30*time.Second, func() time.Time { return now })
	lease, err := store.Claim("profile-a", 7, "")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(20 * time.Second)
	renewed, err := store.Renew("profile-a", 7, lease.Token)
	if err != nil || !renewed.ExpiresAt.Equal(now.Add(30*time.Second)) {
		t.Fatalf("renewed %+v, %v", renewed, err)
	}
	store.RevokeProfile("profile-a")
	if err := store.Check("profile-a", 7, lease.Token); !errors.Is(err, ErrLeaseExpired) {
		t.Fatalf("revoked lease got %v", err)
	}
}

func TestTabLeaseActiveCountDropsAfterExpiration(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	store := NewLeaseStore(30*time.Second, func() time.Time { return now })
	if _, err := store.Claim("profile-a", 7, ""); err != nil {
		t.Fatal(err)
	}
	if store.ActiveCount() != 1 {
		t.Fatal("expected one active lease")
	}
	now = now.Add(31 * time.Second)
	if store.ActiveCount() != 0 {
		t.Fatal("expired lease still counted")
	}
}
