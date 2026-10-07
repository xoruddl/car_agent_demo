package otaclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testSHA256 = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

// newTestServer는 실제 HTTP 요청을 받는 테스트용 OTA 서버를 띄운다.
func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// writeJSON은 가짜 서버가 JSON 응답을 보낼 때 쓰는 도우미다.
func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

// 등록 API는 등록 키와 차량 속성을 받고 토큰을 한 번 발급한다.
func TestRegister(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/vehicles/register" {
			t.Errorf("request = %s %s, want POST /api/v1/vehicles/register", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer enrollment-key" {
			t.Errorf("Authorization = %q", got)
		}
		var got RegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if got.VehicleID != "veh-001" || got.Model != "SIM-A" || got.HWVersion != "TCU-REV2" || got.CurrentVersion != "1.0.0" {
			t.Errorf("request = %+v", got)
		}
		writeJSON(t, w, http.StatusCreated, map[string]any{"vehicleId": "veh-001", "vehicleToken": "token-1", "issuedAt": "2026-10-02T03:10:00Z"})
	})

	res, err := New(srv.URL, time.Second).Register(context.Background(), "enrollment-key", RegisterRequest{VehicleID: "veh-001", Model: "SIM-A", HWVersion: "TCU-REV2", CurrentVersion: "1.0.0"})
	if err != nil || res.VehicleToken != "token-1" {
		t.Errorf("Register() = %+v, %v", res, err)
	}
}

// 체크인은 lastUpdate와 차량 토큰을 보내고, 대상 캠페인과 다음 주기를 받는다.
func TestCheckin(t *testing.T) {
	finishedAt := time.Date(2026, 10, 2, 3, 40, 12, 0, time.UTC)
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/vehicles/veh-001/check-in" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer vehicle-token" {
			t.Errorf("Authorization = %q", got)
		}
		var got CheckinRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if got.LastUpdate == nil || got.LastUpdate.CampaignID != "cmp-old" || got.LastUpdate.FinishedAt == nil || !got.LastUpdate.FinishedAt.Equal(finishedAt) {
			t.Errorf("lastUpdate = %+v", got.LastUpdate)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"updateTarget": true, "campaignId": "cmp-42", "nextCheckInSeconds": 30})
	})

	res, err := New(srv.URL, time.Second).Checkin(context.Background(), "veh-001", "vehicle-token", CheckinRequest{
		Model: "SIM-A", HWVersion: "TCU-REV2", CurrentVersion: "1.1.0",
		LastUpdate: &LastUpdate{CampaignID: "cmp-old", Result: "SUCCEEDED", FinishedAt: &finishedAt},
	})
	if err != nil || !res.UpdateTarget || res.CampaignID != "cmp-42" || res.NextCheckInSeconds != 30 {
		t.Errorf("Checkin() = %+v, %v", res, err)
	}
}

// 매니페스트 요청은 차량 토큰을 보내며, 다운로드·검증 필드를 모두 읽어야 한다.
func TestManifest(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/vehicles/veh-001/campaigns/cmp-42/manifest" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer vehicle-token" {
			t.Errorf("Authorization = %q", got)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"campaignId": "cmp-42", "targetVersion": "1.1.0", "fileSize": 1024, "sha256": testSHA256,
			"signature": "signature-base64", "downloadUrl": "https://cdn.example/firmware.bin", "urlExpiresAt": "2026-10-02T05:15:00Z",
		})
	})

	res, err := New(srv.URL, time.Second).Manifest(context.Background(), "veh-001", "cmp-42", "vehicle-token")
	if err != nil || res.FileSize != 1024 || res.DownloadURL == "" {
		t.Errorf("Manifest() = %+v, %v", res, err)
	}
}

// 401, 409, 410처럼 에이전트가 복구 방법을 달리 정할 상태 코드는 StatusError로 보존한다.
func TestStatusError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusConflict, http.StatusGone} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, status, map[string]string{"code": "SERVER_CODE"})
			})
			_, err := New(srv.URL, time.Second).Manifest(context.Background(), "veh-001", "cmp-42", "token")
			var statusErr *StatusError
			if !errors.As(err, &statusErr) || statusErr.StatusCode != status || statusErr.Code != "SERVER_CODE" {
				t.Errorf("Manifest() error = %v, want StatusError(%d)", err, status)
			}
		})
	}
}

// 잘못된 매니페스트는 다운로드를 시작하기 전에 거부해야 한다.
func TestManifest_InvalidResponse(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"campaignId": "cmp-42"})
	})
	if _, err := New(srv.URL, time.Second).Manifest(context.Background(), "veh-001", "cmp-42", "token"); err == nil {
		t.Error("Manifest() error = nil, want invalid response error")
	}
}
