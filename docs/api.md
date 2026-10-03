# OTA API 계약 (차량 에이전트 기준)

차량 에이전트가 호출하는 API를 정리한다.

| 순서 | 호출 대상 | 요청 | 역할 |
|---|---|---|---|
| 1 | OTA 서버 | `POST /api/v1/vehicles/register` | 차량 등록과 토큰 발급 |
| 2 | OTA 서버 | `POST /api/v1/vehicles/{vehicleId}/check-in` | 주기적 상태 보고, 대상 판정, 직전 결과 보고 |
| 3 | OTA 서버 | `GET /api/v1/vehicles/{vehicleId}/campaigns/{campaignId}/manifest` | 매니페스트와 CDN 서명 URL 조회 |
| 4 | CDN | `GET {downloadUrl}` | 업데이트 파일 다운로드 |

모든 연결은 차량 에이전트가 시작한다. OTA 서버에 보내는 JSON 요청은 `Content-Type: application/json`을 사용한다. 대시보드·관리자 API는 차량 에이전트의 호출 대상이 아니다.

## 1. 차량 등록

`POST /api/v1/vehicles/register`

- 처음 등록하면 `201 Created`, 같은 `vehicleId`로 재등록하면 `200 OK`를 받는다.
- `Authorization: Bearer {enrollment-key}`가 필수다. 등록 키는 환경변수 `ENROLLMENT_KEY`에서 받는다.
- 요청의 `vehicleId`는 환경변수 `VEHICLE_ID`에서 받으며 영문·숫자·`-`만 허용하고 최대 64자다.

요청 예시:

```json
{
  "vehicleId": "veh-001",
  "model": "SIM-A",
  "hwVersion": "TCU-REV2",
  "region": "KR",
  "currentVersion": "1.0.0"
}
```

| 필드 | 필수 | 설명 |
|---|---|---|
| `vehicleId` | 예 | 차량 ID |
| `model` | 예 | 차종 |
| `hwVersion` | 예 | TCU 하드웨어 리비전 |
| `region` | 아니요 | 지역 |
| `currentVersion` | 예 | 현재 설치된 소프트웨어 버전 |

성공 응답에는 `vehicleId`, `vehicleToken`, `issuedAt`이 있다. 토큰 원문은 이 응답에서 한 번만 전달된다. 차량은 토큰을 메모리에만 보관하고, 재시작하면 재등록해 새 토큰을 받는다. 재등록 시 이전 토큰은 폐기된다.

```json
{
  "vehicleId": "veh-001",
  "vehicleToken": "vt_6Jw0r9...base64url...",
  "issuedAt": "2026-10-02T03:10:00.000Z"
}
```

| 상태 | 코드 | 차량 동작 |
|---|---|---|
| `400` | `INVALID_REQUEST` | 필수값과 `vehicleId` 형식을 확인한다. |
| `401` | `INVALID_ENROLLMENT_KEY` | 등록 키가 없거나 틀렸으므로 시작에 실패한다. |
| `500` | `INTERNAL_ERROR` | 재시도한다. |

등록 키는 등록 API에만 쓴다. 토큰 만료 기간과 등록 키 교체 방법은 아직 협의 중이다.

## 2. 체크인과 직전 업데이트 결과 보고

`POST /api/v1/vehicles/{vehicleId}/check-in`

차량은 주기적으로 체크인해 현재 버전과 차량 속성을 알리고 업데이트 대상인지 응답받는다. 직전 업데이트 결과가 있으면 같은 요청의 `lastUpdate`에 담는다. 별도 결과 보고 API는 없다.

- `Authorization: Bearer {vehicle-token}`과 `Content-Type: application/json`이 필수다.
- 경로의 `vehicleId`가 토큰의 차량과 다르면 `403`이다.
- `lastUpdate`가 없으면 `null`을 보낸다. 같은 결과가 두 번 전송돼도 서버는 같은 결과를 유지한다.

요청 예시:

```json
{
  "model": "SIM-A",
  "hwVersion": "TCU-REV2",
  "region": "KR",
  "currentVersion": "1.0.0",
  "lastUpdate": {
    "campaignId": "cmp-20261002-001",
    "result": "FAILED",
    "failureReason": "INSTALL_FAILED",
    "failureDetail": "partition write error",
    "finishedAt": "2026-10-02T03:40:12.000Z"
  }
}
```

| 필드 | 필수 | 설명 |
|---|---|---|
| `model` | 예 | 차종 |
| `hwVersion` | 예 | TCU 하드웨어 리비전 |
| `region` | 아니요 | 지역 |
| `currentVersion` | 예 | 현재 설치된 소프트웨어 버전 |
| `lastUpdate` | 아니요 | 직전 업데이트 결과. 없으면 `null` |
| `lastUpdate.campaignId` | 결과가 있을 때 | 결과를 보고할 캠페인 ID |
| `lastUpdate.result` | 결과가 있을 때 | `SUCCEEDED` 또는 `FAILED` |
| `lastUpdate.failureReason` | `FAILED`일 때 | 아래 실패 사유 중 하나 |
| `lastUpdate.failureDetail` | 아니요 | 최대 500자의 짧은 설명 |
| `lastUpdate.finishedAt` | 아니요 | 차량 기준 종료 시각. 참고용 |

성공 응답은 `200 OK`다. `updateTarget`이 `true`이면 `campaignId`로 매니페스트를 요청한다. `nextCheckInSeconds`는 다음 체크인까지 기다릴 시간(초)이다.

```json
{
  "updateTarget": true,
  "campaignId": "cmp-20261002-001",
  "nextCheckInSeconds": 30
}
```

대상이 아니면 `updateTarget`은 `false`이고 `campaignId`는 없다. 체크인 본문에는 진행률이나 `DOWNLOADING`·`INSTALLING` 같은 단계 필드가 정의되지 않았다.

| 상태 | 코드 | 차량 동작 |
|---|---|---|
| `400` | `INVALID_REQUEST` | 필수 필드와 형식을 확인한다. |
| `401` | `UNAUTHORIZED` | 등록 API부터 다시 호출해 새 토큰을 받는다. |
| `403` | `VEHICLE_MISMATCH` | 경로의 차량 ID와 토큰의 차량이 일치하는지 확인한다. |
| `500` | `INTERNAL_ERROR` | 다음 주기에 재시도한다. |

## 3. 매니페스트 요청

`GET /api/v1/vehicles/{vehicleId}/campaigns/{campaignId}/manifest`

체크인에서 대상이라고 응답받았을 때 호출한다. `campaignId`는 체크인 응답의 값을 쓰며, `Authorization: Bearer {vehicle-token}`이 필수다. 요청 본문과 쿼리 매개변수는 없다.

성공 응답 `200 OK`:

```json
{
  "campaignId": "cmp-20261002-001",
  "targetVersion": "1.1.0",
  "fileSize": 52428800,
  "sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
  "signature": "q8h3...Base64...==",
  "downloadUrl": "http://cdn.example/firmware/SIM-A/1.1.0/firmware.bin?md5=xxx&expires=1791000000",
  "urlExpiresAt": "2026-10-02T05:15:00.000Z"
}
```

| 필드 | 설명 |
|---|---|
| `campaignId` | 캠페인 ID |
| `targetVersion` | 설치할 버전 |
| `fileSize` | 파일 크기(바이트) |
| `sha256` | 다운로드 후 검증할 SHA-256 해시 |
| `signature` | 해시에 대한 Ed25519 서명. 차량 내장 공개키로 검증 |
| `downloadUrl` | CDN 서명 URL |
| `urlExpiresAt` | URL 만료 시각 |

| 상태 | 코드 | 차량 동작 |
|---|---|---|
| `401` | `UNAUTHORIZED` | 등록 API부터 다시 호출한다. |
| `403` | `VEHICLE_MISMATCH` | 경로의 차량 ID와 토큰의 차량이 일치하는지 확인한다. |
| `404` | `CAMPAIGN_NOT_FOUND` | 캠페인이 없다. |
| `409` | `NOT_UPDATE_TARGET` | 차량이 더 이상 대상이 아니다. |
| `410` | `CAMPAIGN_INACTIVE` | 캠페인이 비활성 또는 기간 종료 상태다. |
| `500` | `INTERNAL_ERROR` | 재시도한다. |

서버는 매니페스트 요청 시점에 대상 여부를 다시 확인한다. CDN URL이 만료되면 매니페스트를 다시 요청해 새 URL을 받는다.

## 4. CDN 다운로드와 검증

`GET {downloadUrl}`

매니페스트의 `downloadUrl`을 그대로 요청한다. URL에 CDN 서명값 `md5`와 만료 시각 `expires`가 포함되며, 별도 인증 헤더나 요청 본문은 없다. 성공 응답은 `200 OK`와 파일 바이너리(`application/octet-stream`)다.

| 상태 | 의미 | 차량 동작 |
|---|---|---|
| `403` | CDN 서명 불일치 | 매니페스트를 다시 요청한다. |
| `404` | 파일 없음 | 다운로드 실패로 처리하고 다음 체크인에 `DOWNLOAD_FAILED`를 보고한다. |
| `410` | URL 만료 | 매니페스트를 다시 요청한 뒤 다운로드한다. |
| `502`, `504` | CDN의 Origin 조회 실패 | 재시도한다. 횟수와 간격은 협의 중이다. |

다운로드 후 실제 크기가 `fileSize`와 같은지, SHA-256이 `sha256`과 같은지, `signature`가 차량에 내장된 공개키로 검증되는지 확인한다. 하나라도 실패하면 재시도하거나 매니페스트를 다시 요청한다. 계속 실패하면 업데이트 실패로 보고한다. 재시도 횟수와 간격은 아직 협의 중이다.

## 실패 사유

실패한 업데이트는 다음 체크인의 `lastUpdate.failureReason`에 아래 값을 보낸다.

| 값 | 상황 |
|---|---|
| `DOWNLOAD_FAILED` | CDN 오류, 연결 끊김, 크기 불일치, 재시도 초과 |
| `HASH_MISMATCH` | SHA-256 불일치 |
| `SIGNATURE_INVALID` | Ed25519 서명 검증 실패 |
| `INSTALL_FAILED` | 설치 중 실패 |
