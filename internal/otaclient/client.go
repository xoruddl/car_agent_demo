// Package otaclient는 OTA 서버와 HTTP로 통신한다.
// 차량이 등록·체크인·매니페스트 요청을 먼저 시작하며 서버는 차량에 접속하지 않는다.
package otaclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	vehicleAPIPath   = "/api/v1/vehicles"
	registerAPIPath  = vehicleAPIPath + "/register"
	maxResponseBytes = 1 << 20
)

// Client는 OTA 서버 API를 호출한다.
type Client struct {
	baseURL string
	http    *http.Client
}

// New는 baseURL의 OTA 서버와 통신하는 Client를 만든다.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{baseURL: baseURL, http: &http.Client{Timeout: timeout}}
}

// Register는 등록 키로 차량을 등록하고, 메모리에만 둘 차량 토큰을 받는다.
func (c *Client) Register(ctx context.Context, enrollmentKey string, req RegisterRequest) (RegisterResponse, error) {
	var res RegisterResponse
	if err := c.requestJSON(ctx, http.MethodPost, registerAPIPath, enrollmentKey, req, &res); err != nil {
		return RegisterResponse{}, fmt.Errorf("register: %w", err)
	}
	if err := res.validate(); err != nil {
		return RegisterResponse{}, fmt.Errorf("register: invalid response: %w", err)
	}
	return res, nil
}

// Checkin은 차량 속성·현재 버전·직전 결과를 보내고 업데이트 대상 여부를 받는다.
func (c *Client) Checkin(ctx context.Context, vehicleID, vehicleToken string, req CheckinRequest) (CheckinResponse, error) {
	var res CheckinResponse
	if err := c.requestJSON(ctx, http.MethodPost, checkinPath(vehicleID), vehicleToken, req, &res); err != nil {
		return CheckinResponse{}, fmt.Errorf("check-in: %w", err)
	}
	if err := res.validate(); err != nil {
		return CheckinResponse{}, fmt.Errorf("check-in: invalid response: %w", err)
	}
	return res, nil
}

// Manifest는 대상 캠페인의 다운로드·검증 정보를 요청한다.
func (c *Client) Manifest(ctx context.Context, vehicleID, campaignID, vehicleToken string) (Manifest, error) {
	var res Manifest
	if err := c.requestJSON(ctx, http.MethodGet, manifestPath(vehicleID, campaignID), vehicleToken, nil, &res); err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	if err := res.validate(); err != nil {
		return Manifest{}, fmt.Errorf("manifest: invalid response: %w", err)
	}
	return res, nil
}

// StatusError는 서버가 2xx가 아닌 상태 코드로 응답했다는 에러다.
// Code는 서버가 JSON 오류 코드 필드를 보냈을 때만 채워진다.
type StatusError struct {
	StatusCode int
	Code       string
	Body       string
}

// Error는 상태 코드와 서버 오류 정보를 사람이 읽을 수 있는 문자열로 만든다.
func (e *StatusError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("unexpected status %d (%s): %s", e.StatusCode, e.Code, e.Body)
	}
	return fmt.Sprintf("unexpected status %d: %s", e.StatusCode, e.Body)
}

// requestJSON은 인증 헤더를 붙여 JSON 요청을 보내고 성공 응답을 out에 디코딩한다.
func (c *Client) requestJSON(ctx context.Context, method, path, token string, body, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, maxResponseBytes)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		raw, _ := io.ReadAll(io.LimitReader(limited, 512))
		bodyText := string(bytes.TrimSpace(raw))
		var errorBody struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &errorBody)
		return &StatusError{StatusCode: resp.StatusCode, Code: errorBody.Code, Body: bodyText}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// checkinPath는 차량 ID를 안전하게 이스케이프한 체크인 경로를 만든다.
func checkinPath(vehicleID string) string {
	return vehicleAPIPath + "/" + url.PathEscape(vehicleID) + "/check-in"
}

// manifestPath는 차량·캠페인 ID를 안전하게 이스케이프한 매니페스트 경로를 만든다.
func manifestPath(vehicleID, campaignID string) string {
	return vehicleAPIPath + "/" + url.PathEscape(vehicleID) + "/campaigns/" + url.PathEscape(campaignID) + "/manifest"
}
