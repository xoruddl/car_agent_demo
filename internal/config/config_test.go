// 테스트 파일은 이름이 _test.go로 끝나야 하고, `go test`를 실행할 때만 컴파일된다.
// 같은 package config로 선언했으므로 비공개 함수(valueOr 등)도 직접 테스트할 수 있다.
package config

import (
	"strings"
	"testing"
	"time"
)

// envOf는 map을 getenv 함수로 바꿔 준다.
// Load가 os.Getenv 대신 이 함수를 쓰게 해서, 테스트가 실제 환경변수에 의존하지 않게 한다.
// 실제 환경변수를 바꾸는 테스트는 다른 테스트와 동시에 돌 때 서로 간섭할 수 있다.
func envOf(m map[string]string) func(string) string {
	// 함수 안에서 함수를 만들어 반환한다(클로저). 반환된 함수는 m을 기억하고 있다.
	// map에 없는 키를 조회하면 해당 타입의 제로값인 ""가 나온다.
	return func(key string) string { return m[key] }
}

// 테스트 함수는 이름이 Test로 시작하고 *testing.T 하나를 인자로 받아야 한다.
// `go test ./...`가 이런 함수를 자동으로 찾아 실행한다.

// 필수값만 주면 나머지는 기본값으로 채워지는지 확인한다.
func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load(envOf(map[string]string{
		"VIN":            "KMHEV6000000001",
		"OTA_SERVER_URL": "http://ota-server:8080",
	}))
	if err != nil {
		// t.Fatalf는 실패를 기록하고 이 테스트를 즉시 중단한다.
		// 이후 검사가 의미 없을 때(여기서는 cfg가 비어 있으므로) 쓴다.
		t.Fatalf("Load() error = %v", err)
	}

	want := Config{
		VIN:             "KMHEV6000000001",
		ServerURL:       "http://ota-server:8080",
		CheckinInterval: 30 * time.Second,
		DataDir:         "./data",
		InitialVersion:  "1.0.0",
	}
	// 필드가 모두 비교 가능한 타입이면 구조체를 == 로 통째로 비교할 수 있다.
	if cfg != want {
		// t.Errorf는 실패를 기록하지만 테스트를 계속 진행한다.
		// %+v는 구조체를 필드 이름과 함께 출력해서 어느 필드가 다른지 보기 쉽다.
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

// 모든 항목을 지정하면 기본값 대신 지정한 값이 쓰이는지 확인한다.
func TestLoad_Overrides(t *testing.T) {
	cfg, err := Load(envOf(map[string]string{
		"VIN":              "KMHEV6000000002",
		"OTA_SERVER_URL":   "https://ota.example.com/",
		"CHECKIN_INTERVAL": "5s",
		"DATA_DIR":         "/data",
		"INITIAL_VERSION":  "2.3.4",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := Config{
		VIN:             "KMHEV6000000002",
		ServerURL:       "https://ota.example.com", // 끝의 슬래시는 제거된다
		CheckinInterval: 5 * time.Second,
		DataDir:         "/data",
		InitialVersion:  "2.3.4",
	}
	if cfg != want {
		t.Errorf("Load() = %+v, want %+v", cfg, want)
	}
}

// 잘못된 값이 들어오면 에러를 내고, 에러 메시지에 문제 항목 이름이 들어가는지 확인한다.
//
// 테이블 기반 테스트: 입력과 기대 결과를 표(슬라이스)로 만들어 두고 반복문으로 검사한다.
// Go에서 가장 흔한 테스트 작성 방식으로, 케이스를 추가할 때 표에 한 줄만 넣으면 된다.
func TestLoad_Invalid(t *testing.T) {
	// 올바른 기본 입력. 각 케이스는 여기서 항목 하나만 잘못된 값으로 바꾼다.
	valid := map[string]string{
		"VIN":            "KMHEV6000000001",
		"OTA_SERVER_URL": "http://ota-server:8080",
	}

	// 이름 없는 구조체 타입의 슬라이스. 이 테스트에서만 쓰므로 따로 타입을 선언하지 않는다.
	tests := []struct {
		name    string // 서브테스트 이름 (실패 시 출력에 표시)
		key     string // 잘못된 값으로 바꿀 환경변수
		value   string // 넣을 잘못된 값
		wantErr string // 에러 메시지에 포함되어야 할 문자열
	}{
		{"VIN 누락", "VIN", "", "VIN"},
		{"서버 URL 누락", "OTA_SERVER_URL", "", "OTA_SERVER_URL"},
		{"서버 URL 스킴 없음", "OTA_SERVER_URL", "ota-server:8080", "OTA_SERVER_URL"},
		{"서버 URL 호스트 없음", "OTA_SERVER_URL", "http://", "OTA_SERVER_URL"},
		{"체크인 주기 형식 오류", "CHECKIN_INTERVAL", "30", "CHECKIN_INTERVAL"},
		{"체크인 주기 0", "CHECKIN_INTERVAL", "0s", "CHECKIN_INTERVAL"},
		{"체크인 주기 음수", "CHECKIN_INTERVAL", "-1s", "CHECKIN_INTERVAL"},
	}

	// range는 슬라이스를 순회한다. 첫 번째 값(인덱스)은 쓰지 않으므로 _로 버린다.
	for _, tt := range tests {
		// t.Run은 서브테스트를 만든다. 케이스별로 성공/실패가 따로 표시되고,
		// `go test -run 'TestLoad_Invalid/VIN'`처럼 하나만 골라 실행할 수도 있다.
		t.Run(tt.name, func(t *testing.T) {
			// map은 참조 타입이라 valid를 직접 수정하면 다른 케이스에도 영향이 간다.
			// 그래서 케이스마다 새 map에 복사한 뒤 수정한다.
			env := map[string]string{}
			for k, v := range valid {
				env[k] = v
			}
			env[tt.key] = tt.value

			// 반환값 중 Config는 쓰지 않으므로 _로 버린다.
			_, err := Load(envOf(env))
			if err == nil {
				t.Fatalf("Load() error = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() error = %q, want containing %q", err, tt.wantErr)
			}
		})
	}
}

// 잘못된 항목이 여러 개일 때 첫 번째만이 아니라 모두 한 번에 알려주는지 확인한다.
func TestLoad_ReportsAllErrors(t *testing.T) {
	// 빈 map: 필수값 VIN과 OTA_SERVER_URL이 둘 다 없다.
	_, err := Load(envOf(map[string]string{}))
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	for _, key := range []string{"VIN", "OTA_SERVER_URL"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("Load() error = %q, want containing %q", err, key)
		}
	}
}
