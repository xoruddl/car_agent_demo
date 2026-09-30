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

// newTestServer는 테스트용 가짜 OTA 서버를 띄운다.
//
// httptest.NewServer는 실제 포트(127.0.0.1의 임의 포트)를 열고 handler로 요청을 처리한다.
// 진짜 HTTP 통신을 하므로, 클라이언트 코드를 고치지 않고 그대로 검증할 수 있다.
// t.Cleanup에 Close를 등록해 두면 테스트가 끝날 때 서버가 자동으로 내려간다.
func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper() // 실패 위치를 이 함수가 아니라 호출한 테스트 줄로 표시하게 한다
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

// 체크인 요청이 docs/api.md의 형식(메서드, 경로, 헤더, 본문)대로 나가고,
// 업데이트가 있다는 응답을 제대로 해석하는지 확인한다.
func TestCheckin_UpdateAvailable(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// 요청 형식 검사: 가짜 서버 입장에서 에이전트가 보낸 요청을 들여다본다.
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/vehicles/V1/checkin" {
			t.Errorf("path = %s, want /api/v1/vehicles/V1/checkin", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		var got CheckinRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			// 핸들러는 서버의 별도 goroutine에서 실행된다. t.Fatal은 테스트 goroutine에서만
			// 쓸 수 있으므로, 여기서는 t.Errorf로 기록하고 return으로 빠져나온다.
			t.Errorf("decode request: %v", err)
			return
		}
		want := CheckinRequest{VIN: "V1", CurrentVersion: "1.0.0", State: "IDLE"}
		if got != want {
			t.Errorf("request = %+v, want %+v", got, want)
		}

		writeJSON(t, w, http.StatusOK, map[string]any{
			"update_available": true,
			"update": map[string]any{
				"campaign_id":    "cmp-42",
				"target_version": "1.1.0",
				"url":            "http://cdn/fw/1.1.0.bin",
				"sha256":         testSHA256,
			},
		})
	})

	c := New(srv.URL, time.Second)
	res, err := c.Checkin(context.Background(), CheckinRequest{VIN: "V1", CurrentVersion: "1.0.0", State: "IDLE"})
	if err != nil {
		t.Fatalf("Checkin() error = %v", err)
	}

	if !res.UpdateAvailable || res.Update == nil {
		t.Fatalf("Checkin() = %+v, want update", res)
	}
	want := Update{CampaignID: "cmp-42", TargetVersion: "1.1.0", URL: "http://cdn/fw/1.1.0.bin", SHA256: testSHA256}
	if *res.Update != want {
		t.Errorf("Checkin().Update = %+v, want %+v", *res.Update, want)
	}
}

// 업데이트가 없다는 응답은 UpdateAvailable=false, Update=nil로 해석해야 한다.
func TestCheckin_NoUpdate(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"update_available": false})
	})

	res, err := New(srv.URL, time.Second).Checkin(context.Background(), CheckinRequest{VIN: "V1"})
	if err != nil {
		t.Fatalf("Checkin() error = %v", err)
	}
	if res.UpdateAvailable || res.Update != nil {
		t.Errorf("Checkin() = %+v, want no update", res)
	}
}

// 서버가 이상한 응답을 주면 에러로 처리해야 한다.
// 이런 응답을 그대로 믿으면 받을 URL이나 검증할 해시 없이 다운로드를 시작하게 된다.
func TestCheckin_InvalidResponse(t *testing.T) {
	tests := []struct {
		name string
		body string // 가짜 서버가 보낼 응답 본문(JSON 문자열 그대로)
	}{
		{"JSON 형식 아님", `{not json`},
		{"업데이트가 있다는데 내용이 없음", `{"update_available": true}`},
		{"캠페인 ID 없음", `{"update_available": true, "update": {"target_version": "1.1.0", "url": "http://cdn/a.bin", "sha256": "` + testSHA256 + `"}}`},
		{"목표 버전 없음", `{"update_available": true, "update": {"campaign_id": "c", "url": "http://cdn/a.bin", "sha256": "` + testSHA256 + `"}}`},
		{"URL 없음", `{"update_available": true, "update": {"campaign_id": "c", "target_version": "1.1.0", "sha256": "` + testSHA256 + `"}}`},
		{"SHA256 길이 틀림", `{"update_available": true, "update": {"campaign_id": "c", "target_version": "1.1.0", "url": "http://cdn/a.bin", "sha256": "abc"}}`},
		{"SHA256 대문자", `{"update_available": true, "update": {"campaign_id": "c", "target_version": "1.1.0", "url": "http://cdn/a.bin", "sha256": "9F86D081884C7D659A2FEAA0C55AD015A3BF4F1B2B0B822CD15D6C15B0F00A08"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(tt.body))
			})

			_, err := New(srv.URL, time.Second).Checkin(context.Background(), CheckinRequest{VIN: "V1"})
			if err == nil {
				t.Error("Checkin() error = nil, want error")
			}
		})
	}
}

// 서버가 200이 아닌 상태 코드를 주면 *StatusError로 알려줘야 한다.
// 호출하는 쪽(agent)이 상태 코드를 보고 처리 방법을 고를 수 있게 하기 위해서다.
func TestCheckin_ServerError(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "database is down", http.StatusInternalServerError)
	})

	// "checkin: unexpected status 500: database is down"   ← 바깥 에러 (fmt.Errorf)
	//    └─ *StatusError{StatusCode: 500, ...}               ← 안쪽 에러
	_, err := New(srv.URL, time.Second).Checkin(context.Background(), CheckinRequest{VIN: "V1"})

	// errors.As는 err(또는 그 안에 감싸진 에러) 중 *StatusError 타입이 있으면 statusErr에 꺼내 준다.
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("Checkin() error = %v, want *StatusError", err)
	}
	if statusErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", statusErr.StatusCode)
	}
}

// 서버가 응답하지 않으면 타임아웃으로 끝나야 한다. 무한정 기다리면 에이전트가 멈춘다.
func TestCheckin_Timeout(t *testing.T) {
	// release가 닫힐 때까지 응답하지 않는 서버. 채널(chan)은 goroutine 사이의 신호 통로다.
	// 닫힌 채널에서 받기(<-release)는 즉시 끝나므로, close(release)가 "이제 풀어줘" 신호가 된다.
	release := make(chan struct{})
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		<-release
	})
	// Cleanup은 등록의 역순으로 실행된다. 서버 종료(srv.Close)보다 먼저 핸들러를 풀어 줘야
	// 서버가 "처리 중인 요청이 끝나길" 기다리며 멈추지 않는다.
	// 테스트가 끝난 뒤 서버가 잘 종료되도록 넣은 줄
	t.Cleanup(func() { close(release) })

	start := time.Now()
	// 50ms 안에 응답이 안오면 에러 반환
	_, err := New(srv.URL, 50*time.Millisecond).Checkin(context.Background(), CheckinRequest{VIN: "V1"})
	if err == nil {
		t.Fatal("Checkin() error = nil, want timeout error")
	}

	// 2초 넘게 걸리면 타임아웃이 제대로 걸리지 않은 것으로 판단(더 빨리 종료되어야함) -> 테스트 실패
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("타임아웃까지 %s 걸림, 50ms 근처여야 함", elapsed)
	}
}

// 에이전트가 종료 중이면(ctx 취소) 요청을 바로 멈춰야 한다.
func TestCheckin_ContextCanceled(t *testing.T) {
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"update_available": false})
	})

	// 이미 취소된 context를 만든다.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New(srv.URL, time.Second).Checkin(ctx, CheckinRequest{VIN: "V1"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Checkin() error = %v, want context.Canceled", err)
	}
}

// 서버가 꺼져 있으면(연결 실패) 에러를 반환해야 한다.
func TestCheckin_ServerUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // 주소만 받아 두고 서버를 내린다

	if _, err := New(url, time.Second).Checkin(context.Background(), CheckinRequest{VIN: "V1"}); err == nil {
		t.Error("Checkin() error = nil, want connection error")
	}
}

// 결과 보고 요청이 docs/api.md의 형식대로 나가고, 2xx 응답이면 성공으로 처리하는지 확인한다.
func TestReport(t *testing.T) {
	// 보고는 응답 본문을 쓰지 않으므로 200(본문 있음)과 204(본문 없음) 모두 성공이어야 한다.
	// 같은 검증을 상태 코드만 바꿔 두 번 돌린다(간단한 테이블 테스트).
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		// t.Run은 서브테스트를 만든다. 결과가 "TestReport/OK", "TestReport/No_Content"로
		// 따로 표시되어 어느 경우가 실패했는지 바로 알 수 있다.
		t.Run(http.StatusText(status), func(t *testing.T) {
			// 가짜 OTA 서버를 띄운다. 아래 함수가 핸들러로, 요청이 올 때마다 서버가 이 함수를 호출한다.
			//   r: 클라이언트가 보낸 요청(읽기용)   w: 클라이언트에게 돌려줄 응답(쓰기용)
			// 이 핸들러는 "받은 요청이 API 계약대로인지" 검사한 뒤 응답한다.
			srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
				// 1) 메서드와 경로 검사: VIN이 경로에 들어간 POST여야 한다.
				if r.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", r.Method)
				}
				if r.URL.Path != "/api/v1/vehicles/V1/report" {
					t.Errorf("path = %s, want /api/v1/vehicles/V1/report", r.URL.Path)
				}

				// 2) 본문 검사: 받은 JSON을 구조체로 되돌려(got) 기대값(want)과 비교한다.
				// got은 아래 테스트 본문의 Report(...)에 넘긴 값이 JSON으로 전송되어 도착한 것이다.
				var got ReportRequest
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode request: %v", err) // 핸들러 안이므로 Fatal 대신 Errorf + return
					return
				}
				// want는 어디서 전송받는 값이 아니라, 여기 직접 적어 둔 "정답"이다.
				// 아래 Report(...) 호출에 넘기는 값과 일부러 똑같이 적었다.
				// Report 구현이 필드를 빠뜨리거나 JSON 태그가 틀리면 got이 달라져 여기서 실패한다.
				want := ReportRequest{
					VIN:            "V1",
					CampaignID:     "cmp-42",
					Result:         ResultFailed,
					CurrentVersion: "1.0.0",
					ErrorCode:      "HASH_MISMATCH",
				}
				// 필드가 모두 string이라 구조체를 ==/!=로 통째로 비교할 수 있다.
				// %+v는 필드 이름까지 출력해서 어느 필드가 다른지 보여 준다.
				// 여기서 실패해도 핸들러는 계속 응답하므로 Report()의 err는 nil이 된다.
				// 즉 본문이 틀린 경우의 실패는 이 t.Errorf가 잡는다.
				if got != want {
					t.Errorf("request = %+v, want %+v", got, want)
				}

				// 3) 응답: 200이면 본문을 함께 보내고, 204(No Content)는 본문 없이 보낸다.
				w.WriteHeader(status)
				if status == http.StatusOK {
					w.Write([]byte(`{"ok": true}`))
				}
			})

			// 여기부터는 클라이언트(차량 에이전트) 쪽이다.
			// 가짜 서버 주소로 클라이언트를 만들고, 결과 보고를 한 번 보낸다.
			// 이 값이 JSON으로 전송되어 위 핸들러의 got이 된다.
			// 내용: V1 차량이 캠페인 cmp-42 설치에 해시 불일치로 실패해 1.0.0에 머물러 있다.
			// context.Background()는 취소 조건이 없는 빈 context로, 테스트에서는 중간에 취소할 일이 없어 이걸 쓴다.
			err := New(srv.URL, time.Second).Report(context.Background(), ReportRequest{
				VIN:            "V1",
				CampaignID:     "cmp-42",
				Result:         ResultFailed,
				CurrentVersion: "1.0.0",
				ErrorCode:      "HASH_MISMATCH",
			})
			// 서버가 2xx로 응답했으므로 에러 없이 끝나야 한다.
			// Go는 예외 대신 에러를 반환값으로 돌려주므로 호출한 쪽이 err != nil을 직접 확인한다.
			if err != nil {
				t.Errorf("Report() error = %v", err)
			}
		})
	}
}

// 결과 보고가 실패하면 *StatusError를 반환해야 한다. agent는 이걸 보고 다음 주기에 다시 보낸다.
func TestReport_ServerError(t *testing.T) {
	// 요청 내용과 상관없이 무조건 503(Service Unavailable)으로 응답하는 가짜 서버.
	// 서버가 과부하·점검 등으로 요청을 처리하지 못하는 상황을 흉내 낸다.
	// 요청 형식 검사는 TestReport가 이미 하므로 여기서는 하지 않는다.
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	// 보내는 값은 이 테스트의 관심사가 아니므로 필요한 필드만 채운다.
	// 서버가 503을 주므로 err는 nil이 아니어야 한다.
	err := New(srv.URL, time.Second).Report(context.Background(), ReportRequest{VIN: "V1", CampaignID: "cmp-42", Result: ResultSuccess})

	// 이때 err는 두 겹으로 싸여 있다.
	//   "report: unexpected status 503: "   ← Report가 fmt.Errorf("report: %w", ...)로 감싼 바깥 에러
	//      └─ *StatusError{StatusCode: 503}  ← postJSON이 만든 안쪽 에러
	// 그래서 err를 바로 *StatusError로 볼 수 없고, errors.As로 꺼내야 한다.
	//
	// errors.As는 err를 한 겹씩 벗기며 *StatusError를 찾고, 찾으면 statusErr에 넣고 true를 반환한다.
	// 값을 채워 넣어야 하므로 statusErr의 주소(&statusErr)를 넘긴다.
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Report() error = %v, want *StatusError(503)", err)
	}
}
