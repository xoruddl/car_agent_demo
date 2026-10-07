package otaclient

import (
	"errors"
	"fmt"
	"time"
)

// 이 파일에는 OTA 서버와 주고받는 JSON 요청·응답 타입을 둔다.
// 상태 파일 타입과 분리해 서버 API 변경이 로컬 저장 형식을 직접 바꾸지 않게 한다.

// RegisterRequest는 POST /api/v1/vehicles/register 요청 본문이다.
type RegisterRequest struct {
	VehicleID      string `json:"vehicleId"`
	Model          string `json:"model"`
	HWVersion      string `json:"hwVersion"`
	Region         string `json:"region,omitempty"`
	CurrentVersion string `json:"currentVersion"`
}

// RegisterResponse는 차량 등록 뒤 한 번만 전달되는 차량 토큰을 담는다.
type RegisterResponse struct {
	VehicleID    string    `json:"vehicleId"`
	VehicleToken string    `json:"vehicleToken"`
	IssuedAt     time.Time `json:"issuedAt"`
}

// CheckinRequest는 POST /api/v1/vehicles/{vehicleId}/check-in 요청 본문이다.
type CheckinRequest struct {
	Model          string      `json:"model"`
	HWVersion      string      `json:"hwVersion"`
	Region         string      `json:"region,omitempty"`
	CurrentVersion string      `json:"currentVersion"`
	LastUpdate     *LastUpdate `json:"lastUpdate"`
}

// LastUpdate는 체크인에서 서버에 전달할 직전 업데이트 결과다.
type LastUpdate struct {
	CampaignID    string     `json:"campaignId"`
	Result        string     `json:"result"`
	FailureReason string     `json:"failureReason,omitempty"`
	FailureDetail string     `json:"failureDetail,omitempty"`
	FinishedAt    *time.Time `json:"finishedAt,omitempty"`
}

// CheckinResponse는 대상 여부와 다음 체크인 간격을 담는다.
type CheckinResponse struct {
	UpdateTarget       bool   `json:"updateTarget"`
	CampaignID         string `json:"campaignId,omitempty"`
	NextCheckInSeconds int    `json:"nextCheckInSeconds"`
}

// Manifest는 GET 매니페스트 요청의 성공 응답이다.
type Manifest struct {
	CampaignID    string    `json:"campaignId"`
	TargetVersion string    `json:"targetVersion"`
	FileSize      int64     `json:"fileSize"`
	SHA256        string    `json:"sha256"`
	Signature     string    `json:"signature"`
	DownloadURL   string    `json:"downloadUrl"`
	URLExpiresAt  time.Time `json:"urlExpiresAt"`
}

// validate는 등록 응답이 토큰을 포함하는지 확인한다.
func (r RegisterResponse) validate() error {
	if r.VehicleID == "" {
		return errors.New("vehicleId is empty")
	}
	if r.VehicleToken == "" {
		return errors.New("vehicleToken is empty")
	}
	return nil
}

// validate는 체크인 응답의 대상 여부와 캠페인 ID 조합을 확인한다.
func (r CheckinResponse) validate() error {
	if r.NextCheckInSeconds < 0 {
		return fmt.Errorf("nextCheckInSeconds must not be negative, got %d", r.NextCheckInSeconds)
	}
	if r.UpdateTarget && r.CampaignID == "" {
		return errors.New("campaignId is required when updateTarget is true")
	}
	if !r.UpdateTarget && r.CampaignID != "" {
		return errors.New("campaignId must be empty when updateTarget is false")
	}
	return nil
}

// validate는 다운로드·검증에 필요한 매니페스트 필드가 모두 있는지 확인한다.
func (m Manifest) validate() error {
	switch {
	case m.CampaignID == "":
		return errors.New("campaignId is empty")
	case m.TargetVersion == "":
		return errors.New("targetVersion is empty")
	case m.FileSize <= 0:
		return fmt.Errorf("fileSize must be positive, got %d", m.FileSize)
	case !isLowerHex(m.SHA256, 64):
		return fmt.Errorf("sha256 must be 64 lowercase hex characters, got %q", m.SHA256)
	case m.Signature == "":
		return errors.New("signature is empty")
	case m.DownloadURL == "":
		return errors.New("downloadUrl is empty")
	case m.URLExpiresAt.IsZero():
		return errors.New("urlExpiresAt is empty")
	}
	return nil
}

// isLowerHex는 s가 길이 n의 소문자 16진수 문자열인지 확인한다.
func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}
