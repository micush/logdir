package logs

import (
	"testing"
	"time"
)

// A name or a definite "no name" stands for 30 minutes; a failed lookup is tried again after a minute.
func TestLdDNSFailedLookupIsRetriedSoon(t *testing.T) {
	old := time.Now().Add(-2 * time.Minute)
	if (ldDNSEnt{"", old, ldDNSRetry}).fresh() {
		t.Fatal("a failed lookup must not stand for 30 minutes")
	}
	if !(ldDNSEnt{"sw1.example.com", old, ldDNSTTL}).fresh() || !(ldDNSEnt{"x", old, 0}).fresh() {
		t.Fatal("an answer must stand for 30 minutes")
	}
	if hostPart("cush.example.com.") != "cush" || hostPart("cush") != "cush" {
		t.Fatal("host part")
	}
}
