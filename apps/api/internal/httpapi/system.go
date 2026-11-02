package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The processor and memory of the task (the API and its LibreOffice
// workers) are read every 15 seconds: from the ECS
// task metadata on AWS, otherwise from the cgroup of the container. The
// monitoring keeps one point per minute for 24 hours.

const (
	systemEvery   = 15 * time.Second
	systemMinutes = 24 * 60
)

type systemReading struct {
	cpuSeconds float64 // processor time used since the start, all processors
	memMb      float64
	memLimitMb float64
	cpuLimit   float64 // processors allowed
}

type systemMinute struct {
	t        time.Time
	sum, max float64
	n        int
	mem      float64
}

type systemSampler struct {
	mu       sync.Mutex
	source   string
	last     systemReading
	lastAt   time.Time
	cpuPct   float64
	reading  systemReading
	measured bool
	minutes  []systemMinute
}

func (s *systemSampler) add(r systemReading, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastAt.IsZero() || r.cpuSeconds < s.last.cpuSeconds {
		s.last, s.lastAt, s.reading = r, at, r
		return
	}
	elapsed := at.Sub(s.lastAt).Seconds()
	if elapsed <= 0 {
		return
	}
	limit := r.cpuLimit
	if limit <= 0 {
		limit = float64(runtime.NumCPU())
	}
	pct := min(100, max(0, (r.cpuSeconds-s.last.cpuSeconds)/elapsed/limit*100))
	s.last, s.lastAt, s.reading, s.cpuPct, s.measured = r, at, r, pct, true
	minute := at.UTC().Truncate(time.Minute)
	if n := len(s.minutes); n > 0 && s.minutes[n-1].t.Equal(minute) {
		m := &s.minutes[n-1]
		m.sum, m.n, m.max, m.mem = m.sum+pct, m.n+1, max(m.max, pct), r.memMb
		return
	}
	s.minutes = append(s.minutes, systemMinute{t: minute, sum: pct, max: pct, n: 1, mem: r.memMb})
	if len(s.minutes) > systemMinutes {
		s.minutes = s.minutes[len(s.minutes)-systemMinutes:]
	}
}

func round1(v float64) float32 { return float32(math.Round(v*10) / 10) }

// usage returns what the monitoring shows; nil until two readings.
func (s *systemSampler) usage() *SystemUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.measured {
		return nil
	}
	out := &SystemUsage{CpuPct: round1(s.cpuPct), MemMb: round1(s.reading.memMb), Series: make([]SystemPoint, 0, len(s.minutes))}
	if s.source != "" {
		out.Source = ptr(s.source)
	}
	if s.reading.memLimitMb > 0 {
		out.MemLimitMb = ptr(round1(s.reading.memLimitMb))
	}
	if s.reading.cpuLimit > 0 {
		out.CpuLimit = ptr(float32(s.reading.cpuLimit))
	}
	for _, m := range s.minutes {
		out.Series = append(out.Series, SystemPoint{T: m.t, CpuAvg: round1(m.sum / float64(m.n)), CpuMax: round1(m.max), MemMb: round1(m.mem)})
	}
	return out
}

// recent returns the average processor use and the memory use of the last
// minutes, in percent of the limits, for the alerts; ok is false without
// measures.
func (s *systemSampler) recent(minutes int) (cpu, mem float64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.measured || len(s.minutes) == 0 {
		return 0, 0, false
	}
	from := max(0, len(s.minutes)-minutes)
	sum, n := 0.0, 0
	for _, m := range s.minutes[from:] {
		sum, n = sum+m.sum, n+m.n
	}
	cpu = sum / float64(n)
	if s.reading.memLimitMb > 0 {
		mem = s.reading.memMb / s.reading.memLimitMb * 100
	}
	return cpu, mem, true
}

// WatchSystem reads the processor and memory of the task until ctx ends.
func (s *Server) WatchSystem(ctx context.Context) {
	source, read := systemReader()
	if read == nil {
		s.logger.Info("processor and memory not measured: no ECS metadata nor cgroup")
		return
	}
	s.system.mu.Lock()
	s.system.source = source
	s.system.mu.Unlock()
	tick := time.NewTicker(systemEvery)
	defer tick.Stop()
	failures := 0
	for {
		if r, err := read(ctx); err == nil {
			s.system.add(r, time.Now())
			failures = 0
		} else if failures++; failures == 3 {
			s.logger.Warn("processor and memory not read", "source", source, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// systemReader picks where to read the processor and memory.
func systemReader() (string, func(context.Context) (systemReading, error)) {
	if base := os.Getenv("ECS_CONTAINER_METADATA_URI_V4"); base != "" {
		return "ecs", ecsReader(base)
	}
	if _, err := os.Stat("/sys/fs/cgroup/cpu.stat"); err == nil {
		return "cgroup", cgroupV2
	}
	if _, err := os.Stat("/sys/fs/cgroup/cpuacct/cpuacct.usage"); err == nil {
		return "cgroup", cgroupV1
	}
	return "", nil
}

// TaskCPUs returns the vCPUs of the ECS task, or the CPU limit of the
// container; 0 when there is none. A Fargate task sees more processors than
// it may use.
func TaskCPUs(ctx context.Context) float64 {
	if base := os.Getenv("ECS_CONTAINER_METADATA_URI_V4"); base != "" {
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/task", nil); err == nil {
			if resp, err := http.DefaultClient.Do(req); err == nil {
				defer resp.Body.Close()
				var task struct {
					Limits struct {
						CPU float64 `json:"CPU"`
					} `json:"Limits"`
				}
				if resp.StatusCode == http.StatusOK && json.NewDecoder(resp.Body).Decode(&task) == nil && task.Limits.CPU > 0 {
					return task.Limits.CPU
				}
			}
		}
	}
	if raw, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		quota, period, _ := strings.Cut(strings.TrimSpace(string(raw)), " ")
		q, err1 := strconv.ParseFloat(quota, 64)
		p, err2 := strconv.ParseFloat(period, 64)
		if err1 == nil && err2 == nil && p > 0 {
			return q / p
		}
	}
	return 0
}

// ecsReader reads the stats of the container from the ECS task metadata
// endpoint (no AWS permission needed), and the limits of the task once.
func ecsReader(base string) func(context.Context) (systemReading, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	get := func(ctx context.Context, path string, into any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: status %d", path, resp.StatusCode)
		}
		return json.NewDecoder(resp.Body).Decode(into)
	}
	var limits struct {
		CPU    float64 `json:"CPU"`
		Memory float64 `json:"Memory"`
	}
	var once sync.Once
	return func(ctx context.Context) (systemReading, error) {
		once.Do(func() {
			var task struct {
				Limits struct {
					CPU    float64 `json:"CPU"`
					Memory float64 `json:"Memory"`
				} `json:"Limits"`
			}
			if get(ctx, "/task", &task) == nil {
				limits = task.Limits
			}
		})
		var stats struct {
			CPU struct {
				Usage struct {
					Total float64 `json:"total_usage"`
				} `json:"cpu_usage"`
				Online float64 `json:"online_cpus"`
			} `json:"cpu_stats"`
			Memory struct {
				Usage float64            `json:"usage"`
				Limit float64            `json:"limit"`
				Stats map[string]float64 `json:"stats"`
			} `json:"memory_stats"`
		}
		if err := get(ctx, "/stats", &stats); err != nil {
			return systemReading{}, err
		}
		if stats.CPU.Usage.Total == 0 && stats.Memory.Usage == 0 {
			return systemReading{}, errors.New("no stats yet")
		}
		r := systemReading{cpuSeconds: stats.CPU.Usage.Total / 1e9, cpuLimit: limits.CPU, memLimitMb: limits.Memory}
		r.memMb = (stats.Memory.Usage - cacheBytes(stats.Memory.Stats)) / (1 << 20)
		if r.memLimitMb == 0 && stats.Memory.Limit > 0 && stats.Memory.Limit < 1<<50 {
			r.memLimitMb = stats.Memory.Limit / (1 << 20)
		}
		if r.cpuLimit == 0 {
			r.cpuLimit = stats.CPU.Online
		}
		return r, nil
	}
}

// cacheBytes is the file cache counted in the memory used, which the
// kernel gives back when needed (docker stats leaves it out the same way).
func cacheBytes(stats map[string]float64) float64 {
	for _, key := range []string{"inactive_file", "total_inactive_file"} {
		if v, ok := stats[key]; ok {
			return v
		}
	}
	return stats["cache"]
}

func readNumber(file string) (float64, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
}

func readStats(file string) map[string]float64 {
	out := map[string]float64{}
	f, err := os.Open(file)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if key, value, ok := strings.Cut(sc.Text(), " "); ok {
			if v, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
				out[key] = v
			}
		}
	}
	return out
}

func cgroupV2(context.Context) (systemReading, error) {
	cpu := readStats("/sys/fs/cgroup/cpu.stat")
	usage, ok := cpu["usage_usec"]
	if !ok {
		return systemReading{}, errors.New("cpu.stat without usage_usec")
	}
	mem, err := readNumber("/sys/fs/cgroup/memory.current")
	if err != nil {
		return systemReading{}, err
	}
	r := systemReading{cpuSeconds: usage / 1e6, memMb: (mem - cacheBytes(readStats("/sys/fs/cgroup/memory.stat"))) / (1 << 20)}
	if limit, err := readNumber("/sys/fs/cgroup/memory.max"); err == nil {
		r.memLimitMb = limit / (1 << 20)
	}
	if raw, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		if quota, period, ok := strings.Cut(strings.TrimSpace(string(raw)), " "); ok && quota != "max" {
			q, _ := strconv.ParseFloat(quota, 64)
			p, _ := strconv.ParseFloat(period, 64)
			if q > 0 && p > 0 {
				r.cpuLimit = q / p
			}
		}
	}
	return r, nil
}

func cgroupV1(context.Context) (systemReading, error) {
	usage, err := readNumber("/sys/fs/cgroup/cpuacct/cpuacct.usage")
	if err != nil {
		return systemReading{}, err
	}
	mem, err := readNumber("/sys/fs/cgroup/memory/memory.usage_in_bytes")
	if err != nil {
		return systemReading{}, err
	}
	r := systemReading{cpuSeconds: usage / 1e9, memMb: (mem - cacheBytes(readStats("/sys/fs/cgroup/memory/memory.stat"))) / (1 << 20)}
	if limit, err := readNumber("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil && limit < 1<<50 {
		r.memLimitMb = limit / (1 << 20)
	}
	quota, err1 := readNumber("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
	period, err2 := readNumber("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
	if err1 == nil && err2 == nil && quota > 0 && period > 0 {
		r.cpuLimit = quota / period
	}
	return r, nil
}
