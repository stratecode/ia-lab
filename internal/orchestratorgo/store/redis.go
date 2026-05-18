package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
)

const (
	workerRegistryPrefix = "worker:"
	heartbeatPrefix      = "heartbeat:"
	queueKeyPrefix       = "queue:agent:"
)

var priorityWeights = map[domain.Priority]int{
	domain.PriorityCritical: 0,
	domain.PriorityHigh:     1,
	domain.PriorityNormal:   2,
	domain.PriorityLow:      3,
}

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(opt *redis.Options) *RedisStore {
	return &RedisStore{client: redis.NewClient(opt)}
}

func (s *RedisStore) Close() error {
	return s.client.Close()
}

func (s *RedisStore) Ping(ctx context.Context) error {
	return s.client.Ping(ctx).Err()
}

func (s *RedisStore) EnqueueTask(ctx context.Context, taskID string, agentType domain.AgentType, priority domain.Priority) error {
	score := float64(priorityWeights[priority])*1e12 + float64(time.Now().UnixNano())/1e9
	return s.client.ZAdd(ctx, queueKey(agentType), redis.Z{
		Score:  score,
		Member: taskID,
	}).Err()
}

func (s *RedisStore) DequeueTask(ctx context.Context, agentType domain.AgentType) (string, bool, error) {
	result, err := s.client.ZPopMin(ctx, queueKey(agentType), 1).Result()
	if err != nil {
		return "", false, err
	}
	if len(result) == 0 {
		return "", false, nil
	}
	member, ok := result[0].Member.(string)
	if !ok {
		member = fmt.Sprint(result[0].Member)
	}
	return member, true, nil
}

func (s *RedisStore) QueueDepth(ctx context.Context, agentType domain.AgentType) (int64, error) {
	return s.client.ZCard(ctx, queueKey(agentType)).Result()
}

func (s *RedisStore) TotalQueueDepth(ctx context.Context) (int64, error) {
	var total int64
	for _, agentType := range []domain.AgentType{domain.AgentTypePlanner, domain.AgentTypeCoder, domain.AgentTypeReviewer, domain.AgentTypeInfra, domain.AgentTypeResearcher} {
		depth, err := s.QueueDepth(ctx, agentType)
		if err != nil {
			return 0, err
		}
		total += depth
	}
	return total, nil
}

func (s *RedisStore) CountWorkers(ctx context.Context) (int, error) {
	var count int
	iter := s.client.Scan(ctx, 0, workerRegistryPrefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		count++
	}
	return count, iter.Err()
}

func (s *RedisStore) ListWorkers(ctx context.Context) ([]domain.WorkerInfo, error) {
	workers := make([]domain.WorkerInfo, 0)
	iter := s.client.Scan(ctx, 0, workerRegistryPrefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		workerID := strings.TrimPrefix(key, workerRegistryPrefix)
		registry, err := s.client.HGetAll(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		if len(registry) == 0 {
			continue
		}
		status := registry["status"]
		memoryMB, uptimeS, currentTask := heartbeatData{}, heartbeatData{}, (*string)(nil)
		if value := strings.TrimSpace(registry["current_task"]); value != "" {
			currentTask = &value
		}
		heartbeatRaw, err := s.client.Get(ctx, heartbeatPrefix+workerID).Result()
		if err == nil {
			var heartbeat map[string]any
			if json.Unmarshal([]byte(heartbeatRaw), &heartbeat) == nil {
				memoryMB = parseHeartbeatField(heartbeat["memory_mb"])
				uptimeS = parseHeartbeatField(heartbeat["uptime_s"])
			}
		} else if err == redis.Nil && status == "active" {
			status = "dead"
		} else if err != nil && err != redis.Nil {
			return nil, err
		}
		workers = append(workers, domain.WorkerInfo{
			WorkerID:      workerID,
			Status:        status,
			AgentTypes:    registry["agent_types"],
			CurrentTask:   currentTask,
			RegisteredAt:  stringPtr(registry["registered_at"]),
			LastHeartbeat: stringPtr(registry["last_heartbeat"]),
			MemoryMB:      memoryMB.Float64(),
			UptimeS:       uptimeS.Float64(),
		})
	}
	return workers, iter.Err()
}

func ParseRedisOptions(redisURL string) (*redis.Options, error) {
	return redis.ParseURL(redisURL)
}

func queueKey(agentType domain.AgentType) string {
	return fmt.Sprintf("%s%s", queueKeyPrefix, agentType)
}

func (s *RedisStore) RegisterWorker(ctx context.Context, workerID, agentTypes string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	return s.client.HSet(ctx, workerRegistryPrefix+workerID, map[string]any{
		"agent_types":    agentTypes,
		"status":         "active",
		"current_task":   "",
		"registered_at":  now,
		"last_heartbeat": now,
	}).Err()
}

func (s *RedisStore) UpdateWorkerCurrentTask(ctx context.Context, workerID string, taskID *string) error {
	value := ""
	if taskID != nil {
		value = *taskID
	}
	return s.client.HSet(ctx, workerRegistryPrefix+workerID, "current_task", value).Err()
}

func (s *RedisStore) EmitWorkerHeartbeat(ctx context.Context, workerID string, intervalSeconds int, currentTask *string, startedAt time.Time) error {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	memoryMB := float64(mem.Alloc) / 1024.0 / 1024.0
	uptime := time.Since(startedAt).Seconds()
	payload := map[string]any{
		"worker_id": workerID,
		"task_id":   currentTaskValue(currentTask),
		"memory_mb": roundFloat(memoryMB),
		"uptime_s":  roundFloat(uptime),
		"ts":        time.Now().Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ttlSeconds := int(float64(intervalSeconds)*3.5 + 0.5)
	now := time.Now().UTC().Format(time.RFC3339)
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, heartbeatPrefix+workerID, string(raw), time.Duration(ttlSeconds)*time.Second)
	pipe.HSet(ctx, workerRegistryPrefix+workerID, map[string]any{
		"last_heartbeat": now,
		"current_task":   currentTaskValue(currentTask),
	})
	_, err = pipe.Exec(ctx)
	return err
}

func (s *RedisStore) DeregisterWorker(ctx context.Context, workerID string) error {
	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, workerRegistryPrefix+workerID, "status", "stopped")
	pipe.Del(ctx, heartbeatPrefix+workerID)
	_, err := pipe.Exec(ctx)
	return err
}

type heartbeatData struct {
	valid bool
	value float64
}

func parseHeartbeatField(input any) heartbeatData {
	switch v := input.(type) {
	case float64:
		return heartbeatData{valid: true, value: v}
	case string:
		parsed, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return heartbeatData{valid: true, value: parsed}
		}
	}
	return heartbeatData{}
}

func (h heartbeatData) Float64() *float64 {
	if !h.valid {
		return nil
	}
	value := h.value
	return &value
}

func stringPtr(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	v := value
	return &v
}

func currentTaskValue(taskID *string) string {
	if taskID == nil {
		return ""
	}
	return *taskID
}

func roundFloat(value float64) float64 {
	return float64(int(value*10)) / 10
}

func GenerateWorkerID() string {
	hostname, _ := os.Hostname()
	return fmt.Sprintf("%s-%d-%d", hostname, os.Getpid(), time.Now().Unix())
}
