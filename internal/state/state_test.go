package state

import (
	"strings"
	"testing"
	"time"
)

// validPending은 상태 검증 테스트에서 공통으로 쓰는 완전한 매니페스트다.
func validPending() *PendingUpdate {
	return &PendingUpdate{
		CampaignID:    "cmp-42",
		TargetVersion: "1.1.0",
		FileSize:      52_428_800,
		SHA256:        "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		Signature:     "signature-base64",
		DownloadURL:   "https://cdn.example/firmware.bin?expires=1791000000",
		URLExpiresAt:  time.Date(2026, 10, 2, 5, 15, 0, 0, time.UTC),
	}
}

// validSuccess는 마지막 업데이트 결과가 올바른 성공 값인 예시를 만든다.
func validSuccess() *LastUpdate {
	finishedAt := time.Date(2026, 10, 2, 3, 40, 12, 0, time.UTC)
	return &LastUpdate{
		CampaignID: "cmp-42",
		Result:     ResultSucceeded,
		FinishedAt: &finishedAt,
	}
}

// 상태 머신의 규칙(단계, pending_update, last_update의 짝)이 지켜지는지 검사한다.
func TestState_Validate(t *testing.T) {
	tests := []struct {
		name    string
		state   State
		wantErr bool // true면 에러가 나야 정상
	}{
		{
			name:  "대기 상태",
			state: State{VehicleID: "veh-001", CurrentVersion: "1.0.0", Status: Idle},
		},
		{
			name:  "결과를 보낼 대기 상태",
			state: State{VehicleID: "veh-001", CurrentVersion: "1.1.0", Status: Idle, LastUpdate: validSuccess()},
		},
		{
			name:  "다운로드 중",
			state: State{VehicleID: "veh-001", CurrentVersion: "1.0.0", Status: Downloading, PendingUpdate: validPending()},
		},
		{
			name:  "설치 중",
			state: State{VehicleID: "veh-001", CurrentVersion: "1.0.0", Status: Installing, PendingUpdate: validPending()},
		},
		{
			name:    "차량 ID 없음",
			state:   State{CurrentVersion: "1.0.0", Status: Idle},
			wantErr: true,
		},
		{
			name:    "현재 버전 없음",
			state:   State{VehicleID: "veh-001", Status: Idle},
			wantErr: true,
		},
		{
			name:    "알 수 없는 단계",
			state:   State{VehicleID: "veh-001", CurrentVersion: "1.0.0", Status: "REBOOTING"},
			wantErr: true,
		},
		{
			name:    "대기 상태인데 진행 중인 업데이트가 있음",
			state:   State{VehicleID: "veh-001", CurrentVersion: "1.0.0", Status: Idle, PendingUpdate: validPending()},
			wantErr: true,
		},
		{
			name:    "다운로드 중인데 진행 중인 업데이트가 없음",
			state:   State{VehicleID: "veh-001", CurrentVersion: "1.0.0", Status: Downloading},
			wantErr: true,
		},
		{
			name:    "다운로드 중인데 이전 결과가 남음",
			state:   State{VehicleID: "veh-001", CurrentVersion: "1.0.0", Status: Downloading, PendingUpdate: validPending(), LastUpdate: validSuccess()},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.state.Validate()
			// (err != nil)과 tt.wantErr가 다르면 기대와 다른 결과다.
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// 매니페스트와 결과가 서버 계약의 필수 필드와 값 범위를 지키는지 검사한다.
func TestState_ValidateNestedValues(t *testing.T) {
	tests := []struct {
		name    string
		state   State
		wantErr bool
	}{
		{
			name: "실패 결과",
			state: State{
				VehicleID:      "veh-001",
				CurrentVersion: "1.0.0",
				Status:         Idle,
				LastUpdate: &LastUpdate{
					CampaignID:    "cmp-42",
					Result:        ResultFailed,
					FailureReason: FailureSignatureInvalid,
				},
			},
		},
		{
			name:    "매니페스트 파일 크기 없음",
			state:   State{VehicleID: "veh-001", CurrentVersion: "1.0.0", Status: Downloading, PendingUpdate: &PendingUpdate{}},
			wantErr: true,
		},
		{
			name: "성공 결과에 실패 사유가 있음",
			state: State{VehicleID: "veh-001", CurrentVersion: "1.0.0", Status: Idle, LastUpdate: &LastUpdate{
				CampaignID: "cmp-42", Result: ResultSucceeded, FailureReason: FailureHashMismatch,
			}},
			wantErr: true,
		},
		{
			name: "실패 결과에 실패 사유가 없음",
			state: State{VehicleID: "veh-001", CurrentVersion: "1.0.0", Status: Idle, LastUpdate: &LastUpdate{
				CampaignID: "cmp-42", Result: ResultFailed,
			}},
			wantErr: true,
		},
		{
			name: "실패 상세가 500자 초과",
			state: State{VehicleID: "veh-001", CurrentVersion: "1.0.0", Status: Idle, LastUpdate: &LastUpdate{
				CampaignID: "cmp-42", Result: ResultFailed, FailureReason: FailureInstallFailed, FailureDetail: strings.Repeat("가", 501),
			}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.state.Validate(); (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// 최초 기동용 상태는 대기(IDLE) 상태여야 하고, 그 자체로 올바른 상태여야 한다.
func TestNew(t *testing.T) {
	st := New("veh-001", "1.0.0")

	want := State{VehicleID: "veh-001", CurrentVersion: "1.0.0", Status: Idle}
	// 포인터 필드가 모두 nil이므로 State 전체를 ==로 비교할 수 있다.
	if st != want {
		t.Errorf("New() = %+v, want %+v", st, want)
	}
	if err := st.Validate(); err != nil {
		t.Errorf("New() 결과가 올바르지 않음: %v", err)
	}
}
