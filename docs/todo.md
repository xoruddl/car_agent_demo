# TODO

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
