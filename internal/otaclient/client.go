// Package otaclient는 OTA 서버와 HTTP로 통신한다.
//
// 에이전트는 서버 역할을 하지 않고, 모든 통신을 먼저 시작하는 클라이언트다.
//   - ① 체크인 + ② 매니페스트 수신: Checkin
//   - ④ 결과 보고: Report
//
// ③ 펌웨어 다운로드는 OTA 서버가 아니라 CDN과 통신하므로 downloader 패키지가 맡는다.
// 요청·응답 형식은 docs/api.md 참고.
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

// OTA 서버 API 경로. docs/api.md와 같아야 한다.
//
// 경로는 환경마다 바뀌는 설정값이 아니라 서버와의 약속(API 계약)이라서 코드에 상수로 둔다.
// 환경마다 달라지는 서버 주소는 설정(OTA_SERVER_URL)으로 받는다.
// API 버전이 바뀌면(v2) apiPrefix만 고치면 된다.
const (
	apiPrefix     = "/api/v1/vehicles/"
	actionCheckin = "checkin" // ①② 체크인
	actionReport  = "report"  // ④ 결과 보고
)

// maxResponseBytes는 응답 본문을 최대 몇 바이트까지 읽을지 정한다.
// 체크인 응답은 작은 JSON이다. 서버가 잘못해서 거대한 응답을 보내도
// 에이전트 메모리가 터지지 않도록 상한을 둔다.
const maxResponseBytes = 1 << 20 // 1MB (1을 왼쪽으로 20비트 이동 = 2^20)

// Client는 OTA 서버 API를 호출한다.
type Client struct {
	baseURL string       // 예: "http://ota-server:8080" (끝에 "/" 없음)
	http    *http.Client // 실제 HTTP 요청을 보내는 표준 라이브러리 클라이언트
}

// New는 baseURL의 OTA 서버와 통신하는 Client를 만든다.
//
// timeout은 요청 하나가 연결부터 응답 본문을 다 읽을 때까지 걸릴 수 있는 최대 시간이다.
// 타임아웃이 없으면 서버가 응답하지 않을 때 에이전트가 영원히 멈춰 있게 된다.
// (http.DefaultClient는 타임아웃이 없어서 실무에서는 거의 쓰지 않는다.)
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
	}
}

// Checkin은 서버에 현재 상태를 알리고, 설치할 업데이트가 있는지 물어본다(①②).
//
// ctx(context.Context)는 "이 작업을 그만둬라"는 신호를 전달하는 표준 방법이다.
// 에이전트가 종료 신호(SIGTERM)를 받으면 ctx가 취소되고, 진행 중인 요청도 바로 멈춘다.
// Go에서는 오래 걸릴 수 있는 함수의 첫 번째 인자로 ctx를 받는 것이 관례다.
func (c *Client) Checkin(ctx context.Context, req CheckinRequest) (CheckinResponse, error) {
	var res CheckinResponse
	if err := c.postJSON(ctx, vehiclePath(req.VIN, actionCheckin), req, &res); err != nil {
		return CheckinResponse{}, fmt.Errorf("checkin: %w", err)
	}
	// 서버 응답을 그대로 믿지 않고, 다운로드에 필요한 값이 다 있는지 확인한다.
	if err := res.validate(); err != nil {
		return CheckinResponse{}, fmt.Errorf("checkin: invalid response: %w", err)
	}
	return res, nil
}

// Report는 업데이트 결과를 서버에 보고한다(④).
//
// 서버가 2xx로 답하면 성공이다. 응답 본문은 쓰지 않는다.
// 같은 보고가 두 번 갈 수 있으므로(보고 성공 직후 죽은 경우),
// 서버는 (vin, campaign_id) 기준으로 중복을 안전하게 처리해야 한다.
func (c *Client) Report(ctx context.Context, req ReportRequest) error {
	// 응답 본문이 필요 없으므로 out 자리에 nil을 넘긴다.
	if err := c.postJSON(ctx, vehiclePath(req.VIN, actionReport), req, nil); err != nil {
		return fmt.Errorf("report: %w", err)
	}
	return nil
}

// StatusError는 서버가 2xx가 아닌 상태 코드로 응답했다는 에러다.
//
// 단순 문자열 에러 대신 타입을 따로 두면, 호출하는 쪽에서
// errors.As로 꺼내 상태 코드에 따라 다르게 처리할 수 있다(예: 404와 500을 구분).
type StatusError struct {
	StatusCode int    // HTTP 상태 코드 (예: 500)
	Body       string // 서버가 보낸 응답 본문 앞부분. 원인 파악용
}

// Error 메서드가 있으면 그 타입은 error 인터페이스를 만족한다.
// Go에서는 "implements"를 적지 않아도 메서드 모양만 맞으면 자동으로 인터페이스가 된다.
func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status %d: %s", e.StatusCode, e.Body)
}

// postJSON은 body를 JSON으로 POST하고, 응답이 2xx면 본문을 out에 채운다.
// out이 nil이면 응답 본문은 읽어서 버린다.
//
// body와 out의 타입 any는 "어떤 타입이든 받는다"는 뜻이다(interface{}의 별칭).
// 체크인·보고처럼 요청 모양이 달라도 이 함수 하나로 처리하기 위해 쓴다.
func (c *Client) postJSON(ctx context.Context, path string, body, out any) error {
	payload, err := json.Marshal(body) // Go -> JSON 바이트([]byte)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	// NewRequestWithContext로 만들어야 ctx가 취소될 때 요청도 함께 취소된다.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// 연결 실패, 타임아웃, ctx 취소 등. 서버가 응답 자체를 못 한 경우다.
		return fmt.Errorf("send request: %w", err)
	}
	// LimitReader는 최대 maxResponseBytes까지만 읽게 막아 준다.
	limited := io.LimitReader(resp.Body, maxResponseBytes)

	// 함수가 어떤 경로로 끝나든(에러 응답, 디코드 실패, 성공) 남은 본문을 끝까지 읽어 버린 뒤 닫는다.
	//   - 닫지 않으면 연결과 메모리가 계속 쌓인다(누수).
	//   - 끝까지 읽지 않고 닫으면 HTTP 연결을 다음 요청에 재사용(keep-alive)할 수 없다.
	// limited를 거쳐 읽으므로, 서버가 거대한 본문을 보내도 최대 maxResponseBytes까지만 읽는다.
	//
	// defer func() { ... }()는 익명 함수를 만들어 곧바로 defer로 예약하는 문법이다.
	// 함수가 끝날 때 여러 줄을 실행하고 싶을 때 쓴다.
	// 이 익명 함수는 클로저라서 바깥 변수(limited, resp)를 그대로 참조할 수 있다.
	defer func() {
		_, _ = io.Copy(io.Discard, limited) // 버리는 데이터라 읽기 실패는 무시한다
		resp.Body.Close()
	}()

	// 2xx가 아니면 실패다. 본문 앞부분을 에러에 담아 원인을 알 수 있게 한다.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(limited, 512)) // 에러 메시지용이라 읽기 실패는 무시한다
		return &StatusError{StatusCode: resp.StatusCode, Body: string(bytes.TrimSpace(snippet))}
	}

	if out == nil {
		// 응답 본문이 필요 없다. 남은 본문은 위의 defer가 읽어 버리고 닫는다.
		return nil
	}

	// 본문을 한 번에 메모리로 읽지 않고, 스트림에서 바로 구조체로 디코딩한다.
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// vehiclePath는 "/api/v1/vehicles/{vin}/{action}" 경로를 만든다.
// action에는 위의 action~ 상수를 넘긴다.
//
// url.PathEscape는 VIN에 "/"나 공백 같은 특수문자가 있어도
// 경로가 깨지지 않도록 안전한 형태(%2F 등)로 바꿔 준다.
// (url.JoinPath는 VIN 안의 "/"를 경로 구분자로 처리해 버려서 여기서는 쓰지 않는다.)
func vehiclePath(vin, action string) string {
	return apiPrefix + url.PathEscape(vin) + "/" + action
}
