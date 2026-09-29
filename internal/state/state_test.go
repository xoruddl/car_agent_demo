package state

import "testing"

// 상태 머신의 규칙(단계와 pending_update의 짝)이 지켜지는지 검사하는 Validate를 테스트한다.
func TestState_Validate(t *testing.T) {
	// 진행 중인 업데이트 예시. 포인터가 필요한 곳에서는 &pending으로 주소를 넘긴다.
	pending := PendingUpdate{
		CampaignID:    "cmp-42",
		TargetVersion: "1.1.0",
		URL:           "http://cdn/fw/1.1.0.bin",
		SHA256:        "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
	}

	tests := []struct {
		name    string
		state   State
		wantErr bool // true면 에러가 나야 정상
	}{
		{
			name:  "대기 상태",
			state: State{VIN: "V1", CurrentVersion: "1.0.0", Status: Idle},
		},
		{
			name:  "다운로드 중",
			state: State{VIN: "V1", CurrentVersion: "1.0.0", Status: Downloading, PendingUpdate: &pending},
		},
		{
			name:  "실패 결과 보고 중",
			state: State{VIN: "V1", CurrentVersion: "1.0.0", Status: Reporting, PendingUpdate: &pending, ErrorCode: CodeHashMismatch},
		},
		{
			name:    "VIN 없음",
			state:   State{CurrentVersion: "1.0.0", Status: Idle},
			wantErr: true,
		},
		{
			name:    "현재 버전 없음",
			state:   State{VIN: "V1", Status: Idle},
			wantErr: true,
		},
		{
			name:    "알 수 없는 단계",
			state:   State{VIN: "V1", CurrentVersion: "1.0.0", Status: "REBOOTING"},
			wantErr: true,
		},
		{
			name:    "대기 상태인데 진행 중인 업데이트가 있음",
			state:   State{VIN: "V1", CurrentVersion: "1.0.0", Status: Idle, PendingUpdate: &pending},
			wantErr: true,
		},
		{
			name:    "다운로드 중인데 진행 중인 업데이트가 없음",
			state:   State{VIN: "V1", CurrentVersion: "1.0.0", Status: Downloading},
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

// 최초 기동용 상태는 대기(IDLE) 상태여야 하고, 그 자체로 올바른 상태여야 한다.
func TestNew(t *testing.T) {
	st := New("V1", "1.0.0")

	want := State{VIN: "V1", CurrentVersion: "1.0.0", Status: Idle}
	// PendingUpdate가 포인터라서 State 전체를 ==로 비교하면 "주소"를 비교하게 된다.
	// 여기서는 둘 다 nil이므로 == 비교로 충분하다.
	if st != want {
		t.Errorf("New() = %+v, want %+v", st, want)
	}
	if err := st.Validate(); err != nil {
		t.Errorf("New() 결과가 올바르지 않음: %v", err)
	}
}
