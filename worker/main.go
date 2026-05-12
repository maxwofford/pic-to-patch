package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	rdb       *redis.Client
	resultTTL time.Duration
	ctx       = context.Background()
)

type JobPayload struct {
	JobID          string `json:"job_id"`
	BorderColor    string `json:"border_color"`
	ColorPrecision int    `json:"color_precision"`
	Postprocess    bool   `json:"postprocess"`
}

func main() {
	redisURL := getenv("REDIS_URL", "redis://localhost:6379")
	ttlSec, _ := strconv.Atoi(getenv("RESULT_TTL", "3600"))
	resultTTL = time.Duration(ttlSec) * time.Second

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("bad REDIS_URL: %v", err)
	}
	rdb = redis.NewClient(opt)

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis not reachable: %v", err)
	}
	log.Println("Worker ready, listening on queue 'patches'")

	for {
		result, err := rdb.BRPop(ctx, 0, "patches").Result()
		if err != nil {
			log.Printf("brpop error: %v", err)
			continue
		}

		var job JobPayload
		if err := json.Unmarshal([]byte(result[1]), &job); err != nil {
			log.Printf("bad payload: %v", err)
			continue
		}

		log.Printf("Processing %s", job.JobID)
		if err := processJob(job); err != nil {
			log.Printf("  failed: %v", err)
		} else {
			log.Println("  done")
		}
	}
}

func processJob(job JobPayload) error {
	inputBytes, err := rdb.Get(ctx, fmt.Sprintf("job:%s:input", job.JobID)).Bytes()
	if err != nil {
		return setFailed(job.JobID, "input not found in redis")
	}

	ext, err := rdb.Get(ctx, fmt.Sprintf("job:%s:ext", job.JobID)).Result()
	if err != nil || ext == "" {
		ext = "png"
	}

	resultBytes, err := runPipeline(inputBytes, ext, job.BorderColor, job.ColorPrecision, job.Postprocess)
	if err != nil {
		return setFailed(job.JobID, err.Error())
	}

	pipe := rdb.Pipeline()
	pipe.SetEx(ctx, fmt.Sprintf("job:%s:result", job.JobID), resultBytes, resultTTL)
	pipe.SetEx(ctx, fmt.Sprintf("job:%s:status", job.JobID), "complete", resultTTL)
	pipe.Del(ctx, fmt.Sprintf("job:%s:input", job.JobID))
	_, err = pipe.Exec(ctx)
	return err
}

func setFailed(jobID string, errMsg string) error {
	pipe := rdb.Pipeline()
	pipe.SetEx(ctx, fmt.Sprintf("job:%s:status", jobID), "failed", resultTTL)
	pipe.SetEx(ctx, fmt.Sprintf("job:%s:error", jobID), errMsg, resultTTL)
	pipe.Exec(ctx)
	return fmt.Errorf("%s", errMsg)
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
