package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	maxAttempts    = 4
	initialBackoff = 200 * time.Millisecond
	maxRetryDelay  = 30 * time.Second
)

type Doer interface {
	Do(request *http.Request) (*http.Response, error)
}

func DoJSON(ctx context.Context, client Doer, request *http.Request, target interface{}) error {
	for attempt := 0; attempt < maxAttempts; attempt++ {
		current, err := cloneRequest(ctx, request, attempt)
		if err != nil {
			return err
		}
		response, err := client.Do(current)
		if err != nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt == maxAttempts-1 {
				return fmt.Errorf("cloud API request failed after %d attempts: %w", maxAttempts, err)
			}
			if err := wait(ctx, exponentialBackoff(attempt)); err != nil {
				return err
			}
			continue
		}

		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			limited, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
			_ = response.Body.Close()
			statusError := fmt.Errorf("cloud API returned %s: %s", response.Status, strings.TrimSpace(string(limited)))
			if !retryableStatus(response.StatusCode) || attempt == maxAttempts-1 {
				return statusError
			}
			delay := retryDelay(response.Header.Get("Retry-After"), time.Now(), exponentialBackoff(attempt))
			if err := wait(ctx, delay); err != nil {
				return err
			}
			continue
		}

		defer response.Body.Close()
		if target == nil {
			_, err = io.Copy(io.Discard, response.Body)
			return err
		}
		decoder := json.NewDecoder(response.Body)
		decoder.UseNumber()
		if err := decoder.Decode(target); err != nil {
			return fmt.Errorf("decode cloud API response: %w", err)
		}
		return nil
	}
	return fmt.Errorf("cloud API request exhausted retries")
}

func DefaultClient(client Doer) Doer {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func cloneRequest(ctx context.Context, request *http.Request, attempt int) (*http.Request, error) {
	current := request.Clone(ctx)
	if attempt == 0 || request.Body == nil {
		return current, nil
	}
	if request.GetBody == nil {
		return nil, fmt.Errorf("cloud API request body cannot be replayed safely")
	}
	body, err := request.GetBody()
	if err != nil {
		return nil, fmt.Errorf("recreate cloud API request body: %w", err)
	}
	current.Body = body
	return current, nil
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func exponentialBackoff(attempt int) time.Duration {
	delay := initialBackoff << attempt
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

func retryDelay(value string, now time.Time, fallback time.Duration) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return min(time.Duration(seconds)*time.Second, maxRetryDelay)
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return min(max(deadline.Sub(now), 0), maxRetryDelay)
	}
	return fallback
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
