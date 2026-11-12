package db

import (
	"testing"
	"time"
)

func TestPoolConfigDefaults(t *testing.T) {
	cfg, err := PoolConfig("postgres://u:p@localhost:5432/x?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConns < 10 || cfg.MaxConns > 40 {
		t.Errorf("MaxConns = %d, want 10..40", cfg.MaxConns)
	}
	if cfg.MinConns != 2 || cfg.MaxConnLifetime != time.Hour || cfg.HealthCheckPeriod != 30*time.Second || cfg.MaxConnIdleTime != 15*time.Minute {
		t.Errorf("defaults not applied: %+v", cfg)
	}
}

func TestPoolConfigDSNWins(t *testing.T) {
	cfg, err := PoolConfig("postgres://u:p@localhost:5432/x?sslmode=disable&pool_max_conns=3&pool_min_conns=5&pool_health_check_period=5s")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConns != 3 {
		t.Errorf("MaxConns = %d, want the DSN's 3", cfg.MaxConns)
	}
	if cfg.MinConns != 3 {
		t.Errorf("MinConns = %d, want it clamped to MaxConns", cfg.MinConns)
	}
	if cfg.HealthCheckPeriod != 5*time.Second {
		t.Errorf("HealthCheckPeriod = %v, want the DSN's 5s", cfg.HealthCheckPeriod)
	}
}

func TestPoolConfigIgnoresPoolParameterSubstrings(t *testing.T) {
	base, err := PoolConfig("postgres://u:p@localhost:5432/x?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	got, err := PoolConfig("postgres://u:p@localhost:5432/x?sslmode=disable&xpool_max_conns=2")
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxConns != base.MaxConns {
		t.Errorf("xpool_max_conns changed MaxConns from %d to %d", base.MaxConns, got.MaxConns)
	}
}

func TestDefaultMaxConns(t *testing.T) {
	for cpus, want := range map[int]int32{1: 10, 2: 10, 4: 16, 8: 32, 16: 40, 64: 40} {
		if got := defaultMaxConns(cpus); got != want {
			t.Errorf("defaultMaxConns(%d) = %d, want %d", cpus, got, want)
		}
	}
}
