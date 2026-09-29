// Package state는 에이전트가 디스크에 저장하는 상태(영속 상태)를 정의하고 저장·복구한다.
//
// 에이전트는 서버에 의존하지 않고 스스로 "지금 무엇을 하던 중인지"를 기억해야 한다.
// 그래야 컨테이너가 죽었다 살아나도(차량 전원이 꺼졌다 켜져도) 하던 일을 이어갈 수 있다.
// 상태 모델과 상태 머신 규칙은 docs/vehicle-agent.md 4~5장에 정리되어 있다.
package state

import (
	"errors"
	"fmt"
)

// Status는 상태 머신의 현재 단계다.
//
// `type Status string`은 string을 바탕으로 새 타입을 만든다.
// 그냥 string을 쓰면 아무 문자열이나 넣을 수 있지만, 별도 타입을 두면
// "이 값은 상태 단계"라는 의미가 코드에 드러나고 함수 인자로 잘못 넘기는 실수를 줄여 준다.
type Status string

// 상태 머신의 단계들. 값은 상태 파일과 서버 통신에 그대로 쓰이는 문자열이다.
//
// 흐름: Idle → Downloading → Installing → Reporting → Idle
// 다운로드·설치 중 실패하면 ErrorCode를 기록하고 바로 Reporting으로 간다.
const (
	// Idle은 진행 중인 업데이트가 없는 대기 상태. 주기적으로 서버에 체크인한다.
	Idle Status = "IDLE"

	// Downloading은 펌웨어를 내려받고 SHA256을 검증하는 중인 상태.
	Downloading Status = "DOWNLOADING"

	// Installing은 검증된 펌웨어를 설치하는 중인 상태.
	Installing Status = "INSTALLING"

	// Reporting은 업데이트 결과(성공/실패)를 서버에 보고하는 중인 상태.
	// 보고가 성공할 때까지 이 상태에 머물며 재시도한다.
	// 설치 직후 죽어도 결과를 잃지 않기 위해 별도 단계로 둔다.
	Reporting Status = "REPORTING"
)

// 실패 코드. 실패 시 State.ErrorCode에 기록하고, 결과 보고 때 서버로 보낸다.
// 목록과 의미는 docs/api.md "실패 코드"와 같아야 한다.
const (
	CodeDownloadFailed = "DOWNLOAD_FAILED" // 다운로드 네트워크 오류 또는 200이 아닌 응답
	CodeHashMismatch   = "HASH_MISMATCH"   // 받은 파일의 SHA256이 매니페스트와 다름
	CodeInstallFailed  = "INSTALL_FAILED"  // 설치 중 파일 오류
)

// State는 디스크(state.json)에 저장하는 에이전트의 영속 상태다.
//
// 필드 뒤의 `json:"..."`는 구조체 태그다. encoding/json이 이 태그를 보고
// JSON 필드 이름을 정한다. 태그가 없으면 Go 필드 이름(VIN, CurrentVersion)이 그대로 쓰인다.
// 태그 이름을 바꾸면 이미 저장된 파일을 읽지 못하므로 함부로 바꾸면 안 된다.
type State struct {
	// VIN은 이 상태 파일의 주인인 차량 ID다.
	// 다른 차량의 볼륨을 잘못 붙인 경우를 알아채는 데 쓴다.
	VIN string `json:"vin"`

	// CurrentVersion은 현재 설치되어 동작 중인 펌웨어 버전이다.
	CurrentVersion string `json:"current_version"`

	// Status는 상태 머신의 현재 단계다. JSON에서는 "state"라는 이름으로 저장한다.
	Status Status `json:"state"`

	// PendingUpdate는 진행 중인 업데이트 정보다. Idle일 때는 nil(JSON에서는 null)이다.
	//
	// *PendingUpdate는 포인터 타입이다. 포인터는 nil(없음)을 표현할 수 있어서
	// "진행 중인 업데이트가 있다/없다"를 자연스럽게 나타낸다.
	// 포인터가 아닌 구조체였다면 "빈 값"과 "없음"을 구분하기 어렵다.
	PendingUpdate *PendingUpdate `json:"pending_update"`

	// ErrorCode는 진행 중 발생한 실패 코드다. 실패가 없으면 빈 문자열이다.
	// Reporting 단계에서 이 값이 비어 있으면 SUCCESS, 아니면 FAILED로 보고한다.
	ErrorCode string `json:"error_code"`
}

// PendingUpdate는 서버의 매니페스트(체크인 응답)에서 받은, 진행 중인 업데이트 정보다.
//
// 서버 응답 타입(otaclient 패키지)과 모양이 비슷하지만 일부러 따로 둔다.
// 서버 API가 바뀌어도 디스크 저장 형식이 함께 흔들리지 않게 하기 위해서다.
type PendingUpdate struct {
	CampaignID    string `json:"campaign_id"`    // 배포 캠페인 ID. 결과 보고에 그대로 쓴다
	TargetVersion string `json:"target_version"` // 설치할 버전
	URL           string `json:"url"`            // 펌웨어 다운로드 URL (CDN)
	SHA256        string `json:"sha256"`         // 다운로드 파일 검증용 해시 (소문자 hex)
}

// New는 최초 기동 시 쓸 상태를 만든다. 진행 중인 업데이트가 없는 대기 상태다.
func New(vin, currentVersion string) State {
	return State{
		VIN:            vin,
		CurrentVersion: currentVersion,
		Status:         Idle,
	}
}

// Valid는 s가 정의된 단계 중 하나인지 확인한다.
//
// (s Status)는 리시버다. 이 함수를 Status 타입의 메서드로 만든다.
// 그래서 st.Status.Valid()처럼 값 뒤에 점을 찍어 호출할 수 있다.
func (s Status) Valid() bool {
	switch s {
	case Idle, Downloading, Installing, Reporting: // case 하나에 값 여러 개를 나열할 수 있다
		return true
	default:
		return false
	}
}

// Validate는 상태가 상태 머신 규칙에 맞는지 검사한다.
//
// 저장 직전과 읽은 직후에 호출해서, 코드 버그나 파일 손상으로 생긴
// 말이 안 되는 상태(예: 다운로드 중인데 받을 URL이 없음)로 동작하지 않게 막는다.
func (st State) Validate() error {
	if st.VIN == "" {
		return errors.New("vin is empty")
	}
	if st.CurrentVersion == "" {
		return errors.New("current_version is empty")
	}
	if !st.Status.Valid() {
		return fmt.Errorf("unknown state %q", st.Status)
	}

	// 대기 상태에서만 PendingUpdate가 없고, 나머지 단계에서는 반드시 있어야 한다.
	// 다운로드·설치·보고 모두 "어떤 업데이트"에 대한 작업이기 때문이다.
	switch {
	case st.Status == Idle && st.PendingUpdate != nil:
		return errors.New("pending_update must be null in IDLE state")
	case st.Status != Idle && st.PendingUpdate == nil:
		return fmt.Errorf("pending_update is required in %s state", st.Status)
	}
	return nil
}
