// Package state는 에이전트가 디스크에 저장하는 상태(영속 상태)를 정의하고 저장·복구한다.
//
// 에이전트는 서버에 의존하지 않고 스스로 "지금 무엇을 하던 중인지"를 기억해야 한다.
// 그래야 컨테이너가 죽었다 살아나도(차량 전원이 꺼졌다 켜져도) 하던 일을 이어갈 수 있다.
// 상태 모델과 상태 머신 규칙은 docs/vehicle-agent.md 5~6장에 정리되어 있다.
package state

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// Status는 상태 머신의 현재 단계다.
//
// `type Status string`은 string을 바탕으로 새 타입을 만든다.
// 그냥 string을 쓰면 아무 문자열이나 넣을 수 있지만, 별도 타입을 두면
// "이 값은 상태 단계"라는 의미가 코드에 드러나고 함수 인자로 잘못 넘기는 실수를 줄여 준다.
type Status string

// 상태 머신의 단계들. 값은 상태 파일에 그대로 쓰이는 문자열이다.
const (
	// Idle은 진행 중인 업데이트가 없는 대기 상태다. 주기적으로 서버에 체크인한다.
	Idle Status = "IDLE"

	// Downloading은 펌웨어를 내려받고 크기·SHA-256·서명을 검증하는 중인 상태다.
	Downloading Status = "DOWNLOADING"

	// Installing은 검증된 펌웨어를 설치 위치에 반영하는 중인 상태다.
	Installing Status = "INSTALLING"
)

// 업데이트 결과 값. 성공·실패 결과는 다음 체크인의 lastUpdate로 서버에 전달한다.
const (
	ResultSucceeded = "SUCCEEDED"
	ResultFailed    = "FAILED"
)

// 실패 사유. 목록과 의미는 docs/api.md의 "실패 사유"와 같아야 한다.
const (
	FailureDownloadFailed   = "DOWNLOAD_FAILED"   // CDN 오류, 연결 끊김, 크기 불일치, 재시도 초과
	FailureHashMismatch     = "HASH_MISMATCH"     // 받은 파일의 SHA-256이 매니페스트와 다름
	FailureSignatureInvalid = "SIGNATURE_INVALID" // Ed25519 서명 검증 실패
	FailureInstallFailed    = "INSTALL_FAILED"    // 설치 중 파일 오류
)

// State는 디스크(state.json)에 저장하는 에이전트의 영속 상태다.
//
// 필드 뒤의 `json:"..."`는 구조체 태그다. encoding/json이 이 태그를 보고
// JSON 필드 이름을 정한다. 태그 이름을 바꾸면 이미 저장된 파일을 읽지 못하므로
// API 계약과 함께 신중히 바꾼다.
type State struct {
	// VehicleID는 이 상태 파일의 주인인 차량 ID다.
	// 다른 차량의 볼륨을 잘못 붙인 경우를 알아채는 데 쓴다.
	VehicleID string `json:"vehicle_id"`

	// CurrentVersion은 현재 설치되어 동작 중인 펌웨어 버전이다.
	CurrentVersion string `json:"current_version"`

	// Status는 상태 머신의 현재 단계다. JSON에서는 "state"라는 이름으로 저장한다.
	Status Status `json:"state"`

	// PendingUpdate는 진행 중인 업데이트의 매니페스트 정보다. Idle일 때는 nil(JSON에서는 null)이다.
	// 포인터를 쓰면 "진행 중인 업데이트가 없음"을 nil로 명확하게 표현할 수 있다.
	PendingUpdate *PendingUpdate `json:"pending_update"`

	// LastUpdate는 다음 체크인의 lastUpdate에 담을 직전 업데이트 결과다.
	// 체크인이 200 OK로 끝난 뒤에만 nil로 지운다. 서버가 결과를 기록하기 전에
	// 종료돼도 다음 체크인에서 같은 결과를 다시 보낼 수 있게 하기 위해서다.
	LastUpdate *LastUpdate `json:"last_update"`
}

// PendingUpdate는 서버의 매니페스트에서 받은, 진행 중인 업데이트 정보다.
// 서버 API 타입과 저장 형식을 분리해 서버 계약 변경이 상태 파일을 바로 흔들지 않게 한다.
type PendingUpdate struct {
	// CampaignID는 배포 캠페인 ID다.
	CampaignID string `json:"campaign_id"`

	// TargetVersion은 설치할 펌웨어 버전이다.
	TargetVersion string `json:"target_version"`

	// FileSize는 받아야 하는 펌웨어의 바이트 수다.
	FileSize int64 `json:"file_size"`

	// SHA256은 다운로드한 파일과 비교할 소문자 16진수 SHA-256 해시다.
	SHA256 string `json:"sha256"`

	// Signature는 SHA-256 해시에 대한 Base64 Ed25519 서명이다.
	Signature string `json:"signature"`

	// DownloadURL은 CDN이 발급한 서명 URL이다. 재기동 후에는 만료될 수 있어 다시 받는다.
	DownloadURL string `json:"download_url"`

	// URLExpiresAt은 CDN 서명 URL의 만료 시각이다.
	URLExpiresAt time.Time `json:"url_expires_at"`
}

// LastUpdate는 설치가 끝난 뒤 서버에 한 번 이상 전달할 업데이트 결과다.
type LastUpdate struct {
	// CampaignID는 결과가 속한 캠페인 ID다.
	CampaignID string `json:"campaign_id"`

	// Result는 SUCCEEDED 또는 FAILED다.
	Result string `json:"result"`

	// FailureReason은 실패했을 때만 서버에 보내는 표준 실패 사유다.
	FailureReason string `json:"failure_reason,omitempty"`

	// FailureDetail은 장애 원인을 파악하기 위한 최대 500자의 짧은 설명이다.
	FailureDetail string `json:"failure_detail,omitempty"`

	// FinishedAt은 차량에서 설치가 끝난 시각이다. 없으면 nil로 두고 JSON에서 생략한다.
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// New는 최초 기동 시 쓸 상태를 만든다. 진행 중인 업데이트가 없는 대기 상태다.
func New(vehicleID, currentVersion string) State {
	return State{
		VehicleID:      vehicleID,
		CurrentVersion: currentVersion,
		Status:         Idle,
	}
}

// Valid는 s가 정의된 단계 중 하나인지 확인한다.
//
// (s Status)는 리시버다. 이 함수를 Status 타입의 메서드로 만들면
// st.Status.Valid()처럼 값 뒤에 점을 찍어 호출할 수 있다.
func (s Status) Valid() bool {
	switch s {
	case Idle, Downloading, Installing:
		return true
	default:
		return false
	}
}

// Validate는 상태가 상태 머신 규칙에 맞는지 검사한다.
// 저장 직전과 읽은 직후에 호출해, 파일 손상이나 코드 버그로 생긴 잘못된 상태로 동작하지 않게 막는다.
func (st State) Validate() error {
	if st.VehicleID == "" {
		return errors.New("vehicle_id is empty")
	}
	if st.CurrentVersion == "" {
		return errors.New("current_version is empty")
	}
	if !st.Status.Valid() {
		return fmt.Errorf("unknown state %q", st.Status)
	}

	// 대기 상태에서만 진행 중인 매니페스트가 없어야 한다.
	switch {
	case st.Status == Idle && st.PendingUpdate != nil:
		return errors.New("pending_update must be null in IDLE state")
	case st.Status != Idle && st.PendingUpdate == nil:
		return fmt.Errorf("pending_update is required in %s state", st.Status)
	}
	if st.PendingUpdate != nil {
		if err := st.PendingUpdate.Validate(); err != nil {
			return fmt.Errorf("pending_update: %w", err)
		}
	}

	// 결과는 실패·성공 처리가 끝나 IDLE로 돌아온 뒤에만 남긴다.
	if st.LastUpdate != nil && st.Status != Idle {
		return fmt.Errorf("last_update is allowed only in %s state", Idle)
	}
	if st.LastUpdate != nil {
		if err := st.LastUpdate.Validate(); err != nil {
			return fmt.Errorf("last_update: %w", err)
		}
	}
	return nil
}

// Validate는 PendingUpdate가 재시작 뒤 작업을 재개하기에 충분한지 확인한다.
func (u PendingUpdate) Validate() error {
	switch {
	case u.CampaignID == "":
		return errors.New("campaign_id is empty")
	case u.TargetVersion == "":
		return errors.New("target_version is empty")
	case u.FileSize <= 0:
		return fmt.Errorf("file_size must be positive, got %d", u.FileSize)
	case !isLowerHex(u.SHA256, 64):
		return fmt.Errorf("sha256 must be 64 lowercase hex characters, got %q", u.SHA256)
	case u.Signature == "":
		return errors.New("signature is empty")
	case u.DownloadURL == "":
		return errors.New("download_url is empty")
	case u.URLExpiresAt.IsZero():
		return errors.New("url_expires_at is empty")
	}
	return nil
}

// Validate는 LastUpdate가 체크인 API의 lastUpdate 규칙에 맞는지 확인한다.
func (u LastUpdate) Validate() error {
	if u.CampaignID == "" {
		return errors.New("campaign_id is empty")
	}
	if length := utf8.RuneCountInString(u.FailureDetail); length > 500 {
		return fmt.Errorf("failure_detail must be at most 500 characters, got %d", length)
	}

	switch u.Result {
	case ResultSucceeded:
		if u.FailureReason != "" || u.FailureDetail != "" {
			return errors.New("successful result must not have failure fields")
		}
	case ResultFailed:
		if !validFailureReason(u.FailureReason) {
			return fmt.Errorf("unknown failure_reason %q", u.FailureReason)
		}
	default:
		return fmt.Errorf("unknown result %q", u.Result)
	}
	return nil
}

// validFailureReason은 서버 API가 허용하는 실패 사유인지 확인한다.
func validFailureReason(reason string) bool {
	switch reason {
	case FailureDownloadFailed, FailureHashMismatch, FailureSignatureInvalid, FailureInstallFailed:
		return true
	default:
		return false
	}
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
