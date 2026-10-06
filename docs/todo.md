# TODO

## Notion 차량 API 계약 전환

### 목적

Notion의 `API 설계`와 `도메인별 테이블`에 맞춰 차량 에이전트의 Go 구현을 전환한다.
차량이 호출하는 API는 등록, 체크인, 매니페스트 요청, CDN 다운로드뿐이다.
대시보드·관리자·캠페인 관리 API는 차량 에이전트의 범위가 아니다.

현재 구현에는 이전 계약의 `VIN`, `/checkin`, `/report`, `REPORTING` 상태가 남아 있다.
새 계약은 차량 ID 기반 경로와 `lastUpdate`를 체크인에 함께 보내는 방식이므로,
아래 단위로 교체한다.

### 전환 원칙

- 결과는 별도 보고 API로 보내지 않는다. 업데이트 결과를 상태 파일에 먼저 저장하고,
  다음 체크인의 `lastUpdate`로 보낸다.
- 체크인이 `200 OK`로 끝난 뒤에만 저장된 `lastUpdate`를 제거한다. 응답 전에 종료되면
  같은 결과를 다시 보내며, 서버는 이를 멱등하게 처리한다.
- 차량 토큰은 메모리에만 둔다. 기동 시 재등록하고, 체크인·매니페스트의 `401`은 재등록으로 복구한다.
- 상태 전이는 다음 상태를 영속 저장한 뒤에 실제 다운로드·설치 동작을 시작한다.
- 다운로드는 파일 전체를 메모리에 올리지 않고 스트리밍한다. 크기, SHA-256, Ed25519 서명을 모두 검증한다.

### 구현 단위

#### 1. 차량 설정 계약 전환

대상: `internal/config/config.go`, `internal/config/config_test.go`

- [ ] `VIN`을 `VEHICLE_ID`로 바꾸고 영문·숫자·`-`, 최대 64자를 검증한다.
- [ ] `VEHICLE_MODEL`, `HW_VERSION`, `REGION`을 설정에 추가한다.
- [ ] 등록용 `ENROLLMENT_KEY`와 서명 검증용 `MANIFEST_PUBLIC_KEY`를 추가한다.
- [ ] 기존 `OTA_SERVER_URL`, `CHECKIN_INTERVAL`, `DATA_DIR`, `INITIAL_VERSION`의 동작을 유지한다.
- [ ] 누락값·잘못된 차량 ID·공개키 형식을 테스트한다.

#### 2. 영속 상태 모델 전환

대상: `internal/state/state.go`, `internal/state/store.go`, 해당 테스트

- [ ] 상태 파일의 차량 식별자를 `vehicle_id`로 바꾼다.
- [ ] `last_update`를 추가한다. `campaign_id`, `result`, `failure_reason`, `failure_detail`, `finished_at`을 저장한다.
- [ ] `pending_update`에 `file_size`, `sha256`, `signature`, `download_url`, `url_expires_at`을 저장한다.
- [ ] 상태를 `IDLE`, `DOWNLOADING`, `INSTALLING`으로 정리하고 이전 `REPORTING`, `error_code` 모델을 제거한다.
- [ ] 실패 사유에 `SIGNATURE_INVALID`를 포함하고, 상태별 필수 필드 검증을 보강한다.
- [ ] 원자적 저장·복구·잘못된 상태 거부 테스트를 새 JSON 형식에 맞춘다.

#### 3. OTA 서버 클라이언트 계약 전환

대상: `internal/otaclient/types.go`, `internal/otaclient/client.go`, 해당 테스트

- [ ] `POST /api/v1/vehicles/register` 요청·응답 타입과 등록 키 인증을 구현한다.
- [ ] `POST /api/v1/vehicles/{vehicleId}/check-in` 요청에 차량 속성, 현재 버전, 선택 `lastUpdate`를 담는다.
- [ ] 체크인 응답의 `updateTarget`, `campaignId`, `nextCheckInSeconds`를 검증한다.
- [ ] `GET /api/v1/vehicles/{vehicleId}/campaigns/{campaignId}/manifest`와 매니페스트 응답 타입을 구현한다.
- [ ] 토큰이 필요한 요청에 `Authorization: Bearer {vehicleToken}`을 설정한다.
- [ ] HTTP 상태 코드와 서버 오류 코드를 호출자가 분기할 수 있는 오류 타입으로 보존한다.
- [ ] 등록 `201`·재등록 `200`, 인증 실패, 대상 해제(`409`), 비활성 캠페인(`410`)을 `httptest`로 검증한다.

#### 4. 다운로드·서명 검증 보강

대상: `internal/downloader/`, 새 `internal/verifier/`, 해당 테스트

- [ ] 다운로드 결과의 실제 바이트 수를 매니페스트 `fileSize`와 비교한다.
- [ ] 기존 SHA-256 스트리밍 검증을 매니페스트 값 기준으로 유지한다.
- [ ] SHA-256 해시에 대한 Ed25519 서명을 `MANIFEST_PUBLIC_KEY`로 검증하는 부품을 추가한다.
- [ ] CDN `403`, `410`은 실패 결과를 만들지 않고 매니페스트 재요청 대상으로 구분한다.
- [ ] 네트워크 오류·크기 불일치·해시 불일치·서명 오류를 각각 올바른 실패 사유로 변환한다.

#### 5. 에이전트 상태 머신 전환

대상: `internal/agent/`의 상태 머신과 테스트

- [ ] 기동 시 등록해 토큰을 메모리에 둔다.
- [ ] `IDLE`에서 체크인하고, `200 OK` 뒤 전달 완료된 `last_update`를 제거한다.
- [ ] 업데이트 대상이면 매니페스트를 받은 뒤 `pending_update`와 `DOWNLOADING` 상태를 저장한다.
- [ ] 다운로드·검증 성공 시 `INSTALLING`을 저장하고, 설치 성공 시 버전과 성공 결과를 저장한 뒤 `IDLE`로 전환한다.
- [ ] 각 실패는 실패 결과와 `IDLE`을 함께 저장해 다음 체크인에서 보고한다.
- [ ] 재기동 시 `DOWNLOADING`은 매니페스트를 다시 받아 처음부터 받고, `INSTALLING`은 설치를 다시 수행한다.
- [ ] 체크인·매니페스트 `401`에서 재등록 후 요청을 재개한다.

#### 6. 실행 조립과 종단 간 검증

대상: `cmd/agent/main.go`, 필요한 조립 코드와 테스트

- [ ] 설정, 상태 저장소, OTA 클라이언트, 다운로더, 검증기, 설치기를 조립한다.
- [ ] 종료 신호가 `context` 취소로 HTTP 요청과 루프에 전달되게 한다.
- [ ] `httptest` OTA 서버와 CDN으로 등록 → 체크인 → 매니페스트 → 다운로드·검증 → 설치 → 다음 체크인 결과 보고 흐름을 검증한다.
- [ ] `go vet ./...`, `go test ./...`, `gofmt -l .`를 통과시킨다.

### 권장 커밋 순서

| 순서 | 커밋 메시지 | 포함 파일 수 |
|---:|---|---:|
| 1 | `feat: 노션 기준 차량 설정 로딩` | 2 |
| 2 | `feat: 업데이트 결과 영속 상태 추가` | 4 이하 |
| 3 | `feat: 차량 등록 체크인 매니페스트 클라이언트` | 3 |
| 4 | `feat: 펌웨어 크기와 서명 검증` | 4 이하 |
| 5 | `feat: 노션 계약 상태 머신 실행` | 5 이하 |
| 6 | `test: 노션 계약 OTA 전체 흐름 검증` | 3 이하 |

## 대시보드용 차량 상태 보고 API 추가

### 문제

OTA 서버가 프론트엔드 대시보드에 차량 상태를 실시간으로 보여주려 해도(Redis Pub/Sub + SSE),
현재 API 계약으로는 서버가 차량의 진행 상태를 알 수 없다.

- ① 체크인은 `IDLE`에서만 보내므로 `state`는 항상 `IDLE`이다.
- ④ 결과 보고는 모든 과정이 끝난 뒤 한 번만 보낸다.
- 따라서 `DOWNLOADING`·`INSTALLING` 중에는 서버와 통신이 없어 "다운로드 중" 같은 상태나 진행률을 표시할 수 없다.
- 차량이 꺼졌는지(오프라인)도 판단할 근거가 부족하다.

### 해결 방향

에이전트가 먼저 상태를 알리는 API를 추가한다. 에이전트가 먼저 연결한다는 원칙(AGENTS.md 4번)은 그대로 지킨다.

```
POST /api/v1/vehicles/{vin}/status
{ "state": "DOWNLOADING", "campaign_id": "cmp-42", "progress": 0.42, "current_version": "1.0.0" }
```

- 상태가 바뀔 때마다 보낸다. 다음 상태를 파일에 저장한 직후에 보낸다.
- 다운로드 진행률은 일정 간격으로만 보낸다(예: 2초마다 또는 10% 단위).
- 이 요청은 실패해도 재시도하지 않고 무시한다. 대시보드용 정보라서 OTA 동작을 막으면 안 된다.
- IDLE 체크인을 heartbeat로 보고, 서버는 `last_seen`으로 오프라인 여부를 판단한다.

### 할 일

- [ ] `docs/api.md`에 상태 보고 API 계약 추가(요청 필드, 응답, 전송 주기, 실패 시 처리)
- [ ] `docs/vehicle-agent.md` 상태 머신 설명에 상태 보고 시점 반영
- [ ] `internal/otaclient`에 `ReportStatus` 추가 + `httptest` 테스트
- [ ] `internal/downloader`에 진행률 콜백 추가(스트리밍 유지)
- [ ] `internal/agent` 루프에서 상태 전이 직후 `ReportStatus` 호출

적용 시점: 구현 계획 6번(agent 루프) 커밋을 마친 뒤 별도 커밋으로 진행한다.
OTA 핵심 흐름(①~④)이 먼저 도는 것을 확인한 뒤, 필요한 부품이 다 갖춰진 상태에서 한 번에 붙이기 위해서다.
