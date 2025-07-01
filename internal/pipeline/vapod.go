package pipeline

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iamharshvirani/simulate-ratelimiting/pkg/ratelimiter"
)

type VAPipelinePod struct {
	mu               sync.Mutex
	id               string
	capacityPerSec   int
	processedThisSec int
	lastTickUnix     int64
	rateLimiter      ratelimiter.DynamicRateLimiter
	totalProcessed   uint64 // monotonic counter for accurate metrics
}

func NewVAPipelinePod(id string, capacity int, ctx context.Context, logger *ratelimiter.SimpleLogger) *VAPipelinePod {
	return &VAPipelinePod{
		id:             id,
		capacityPerSec: capacity,
		lastTickUnix:   time.Now().Unix(),
		rateLimiter: ratelimiter.NewDynamicRateLimiter(
			ctx,
			logger,
			"pod_"+id,
			125,   // capacity
			125.0, // initial rate
			60.0,  // min rate
			125.0, // max rate
			5,     // adjust step
			7,     // backoff step
		),
	}
}

func (p *VAPipelinePod) Process() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	currentSecond := now.Unix()

	if p.lastTickUnix != currentSecond {
		p.lastTickUnix = currentSecond
		p.processedThisSec = 0
	}

	if p.processedThisSec < p.capacityPerSec {
		p.processedThisSec++
		atomic.AddUint64(&p.totalProcessed, 1)
		return true
	}
	return false
}

// GetRateLimiter returns the rate limiter instance used by this pod.
func (p *VAPipelinePod) GetRateLimiter() ratelimiter.DynamicRateLimiter {
	return p.rateLimiter
}

// GetTotalProcessed returns the monotonic processed counter.
func (p *VAPipelinePod) GetTotalProcessed() uint64 {
	return atomic.LoadUint64(&p.totalProcessed)
}

func (p *VAPipelinePod) GetId() string {
	return p.id
}

// PipelineCluster manages multiple VA Pipeline pods
type PipelineCluster struct {
	pods []*VAPipelinePod
}

func (c *PipelineCluster) GetPods() []*VAPipelinePod {
	return c.pods
}

func NewPipelineCluster(numPods int, capacityPerPod int, ctx context.Context, logger *ratelimiter.SimpleLogger) *PipelineCluster {
	cluster := &PipelineCluster{
		pods: make([]*VAPipelinePod, numPods),
	}
	for i := 0; i < numPods; i++ {
		cluster.pods[i] = NewVAPipelinePod(fmt.Sprintf("pod-%d", i), capacityPerPod, ctx, logger)
	}
	return cluster
}

func (c *PipelineCluster) GetPod(cameraId string) *VAPipelinePod {
	podIndex := int(cameraHash(cameraId)) % len(c.pods)
	return c.pods[podIndex]
}

// Hash function for camera ID distribution
func cameraHash(cameraId string) uint32 {
	h := uint32(0)
	for i := 0; i < len(cameraId); i++ {
		h = h*31 + uint32(cameraId[i])
	}
	return h
}
