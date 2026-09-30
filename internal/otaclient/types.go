package otaclient

import (
	"errors"
	"fmt"
)

// 이 파일에는 OTA 서버와 주고받는 JSON의 모양(요청·응답 구조체)을 모아 둔다.
// Java의 DTO에 해당하며, 형식은 docs/api.md와 같아야 한다.
//
// state 패키지의 State·PendingUpdate와 모양이 비슷하지만 일부러 따로 둔다.
// 서버 API가 바뀌어도 디스크 저장 형식이 흔들리지 않게 하기 위해서다.
// 두 타입 사이의 변환은 둘을 모두 아는 agent 패키지에서 한다.

// 결과 보고(ReportRequest.Result)에 쓰는 값.
const (
	ResultSuccess = "SUCCESS"
	ResultFailed  = "FAILED"
)

// CheckinRequest는 ① 체크인 요청 본문이다.
// POST /api/v1/vehicles/{vin}/checkin
type CheckinRequest struct {
	VIN            string `json:"vin"`
	CurrentVersion string `json:"current_version"` // 현재 설치된 버전
	// State는 에이전트의 현재 상태 단계다(예: "IDLE").
	// state.Status 타입 대신 string을 써서 이 패키지가 state 패키지에 의존하지 않게 한다.
	State string `json:"state"`
}

// CheckinResponse는 ② 체크인 응답(매니페스트)이다.
type CheckinResponse struct {
	// UpdateAvailable은 이 차량이 설치해야 할 업데이트가 있는지 여부다.
	UpdateAvailable bool `json:"update_available"`

	// Update는 업데이트 내용이다. 업데이트가 없으면 응답에 없거나 null이고, 그때 nil이 된다.
	// 포인터로 선언해야 "없음(nil)"과 "빈 값"을 구분할 수 있다.
	Update *Update `json:"update,omitempty"`
}

// Update는 설치할 업데이트 정보다.
type Update struct {
	CampaignID    string `json:"campaign_id"`    // 배포 캠페인 ID. 결과 보고에 그대로 사용
	TargetVersion string `json:"target_version"` // 설치할 버전
	URL           string `json:"url"`            // 펌웨어 다운로드 URL (CDN)
	SHA256        string `json:"sha256"`         // 펌웨어 SHA256, 소문자 hex 64자
}

// ReportRequest는 ④ 결과 보고 요청 본문이다.
// POST /api/v1/vehicles/{vin}/report
type ReportRequest struct {
	VIN            string `json:"vin"`
	CampaignID     string `json:"campaign_id"`     // ②에서 받은 캠페인 ID
	Result         string `json:"result"`          // ResultSuccess 또는 ResultFailed
	CurrentVersion string `json:"current_version"` // 보고 시점에 설치된 버전 (실패하면 기존 버전)
	ErrorCode      string `json:"error_code"`      // 실패 코드. 성공이면 빈 문자열
}

// validate는 서버가 준 체크인 응답을 믿고 써도 되는지 검사한다.
//
// JSON을 구조체로 바꿀 때 빠진 필드는 에러 없이 제로값("")으로 채워진다.
// 그래서 "필수 필드가 비어 있음"은 이렇게 직접 확인해야 한다.
func (r CheckinResponse) validate() error {
	if !r.UpdateAvailable {
		return nil // 업데이트가 없으면 더 볼 것이 없다
	}
	if r.Update == nil {
		return errors.New("update_available is true but update is missing")
	}
	return r.Update.validate()
}

// validate는 업데이트 정보의 필수 필드와 SHA256 형식을 검사한다.
func (u Update) validate() error {
	// errors.Join처럼 모아서 알려줄 수도 있지만, 서버 응답 오류는
	// 에이전트가 고칠 수 있는 것이 아니므로 첫 번째 문제만 알려도 충분하다.
	switch {
	case u.CampaignID == "":
		return errors.New("update.campaign_id is empty")
	case u.TargetVersion == "":
		return errors.New("update.target_version is empty")
	case u.URL == "":
		return errors.New("update.url is empty")
	case !isLowerHex(u.SHA256, 64):
		return fmt.Errorf("update.sha256 must be 64 lowercase hex characters, got %q", u.SHA256)
	}
	return nil
}

// isLowerHex는 s가 길이 n의 소문자 16진수 문자열인지 확인한다.
// SHA256 해시는 32바이트 = 16진수 64자다.
func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	// range로 문자열을 순회하면 한 글자(rune)씩 꺼낸다.
	for _, c := range s {
		// '0'처럼 작은따옴표는 문자 하나(rune)를 뜻한다. 문자도 숫자처럼 크기 비교가 된다.
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}
