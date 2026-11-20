# TripGo — Trip Service

Сервис создаёт и завершает поездки, сохраняет координаты и возвращает маршрут.
Поездки без свежих координат опрашиваются через Push Service. Данные и история
статусов хранятся в PostgreSQL. Контракт — [OpenAPI](contracts/openapi/trip-service.openapi.yaml).

## Запуск

Нужны Go 1.26.2+, Make, Docker и
[tripgoctl](https://github.com/course-go-autumn-2026/course-infra#установка).
Из корня чистого checkout:

```bash
tripgoctl cluster start       # если кластер ещё не запущен
tripgoctl environment start   # environment.toml: ЛР3
tripgoctl connect             # адреса БД, Push, коллектора и веб-интерфейсов
make generate
make migrate-status
make migrate
make run
```

`make run` собирает `bin/trip-service` и запускает его с `-local-env`.
Приоритет настроек: окружение процесса → `.env` → `.env.example`.
Проверьте, что переменные процесса не перекрывают адреса текущей ЛР3 значениями
от другой работы. `.env` создаёт tripgoctl; заменять его примером не нужно.
Dotenv читается как данные. Без `-local-env` используется только окружение процесса.

Миграции при старте сервиса не применяются. `make migrate-status` показывает
состояние, `make migrate` применяет недостающие up, `make migrate-down` откатывает
одну миграцию с удалением её структур и данных. Откаты проверяйте на отдельной БД.

## Настройки

Все параметры обязательны; `.env.example` содержит безопасные defaults.
Пустые адреса нужно получить из `.env` текущего окружения tripgoctl.

| Переменная | Default / назначение |
|---|---|
| `HTTP_ADDR` | `:8080` |
| `LOG_LEVEL` | `info`; также debug, warn, error |
| `SHUTDOWN_TIMEOUT` | `10s`, общий бюджет остановки всех компонентов |
| `HTTP_READ_TIMEOUT` | `10s` |
| `HTTP_READ_HEADER_TIMEOUT` | `5s`, не больше READ_TIMEOUT |
| `HTTP_WRITE_TIMEOUT` | `10s` |
| `HTTP_IDLE_TIMEOUT` | `60s` |
| `DATABASE_URL` | пусто; PostgreSQL из `.env` ЛР3 |
| `DATABASE_MAX_CONNS` / `DATABASE_MIN_CONNS` | `10` / `2` |
| `DATABASE_MAX_CONN_LIFETIME` | `30m`, больше CONNECT_TIMEOUT |
| `DATABASE_CONNECT_TIMEOUT` | `5s`, подключение и стартовый Ping |
| `DATABASE_QUERY_TIMEOUT` | `3s`, меньше WRITE_TIMEOUT |
| `OTEL_SERVICE_NAME` | `trip-service` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | пусто; коллектор из `.env` ЛР3 |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf`; также grpc |
| `OTEL_RESOURCE_ATTRIBUTES` | `service.namespace=tripgo,deployment.environment=local` |
| `OTEL_TRACES_SAMPLER` | `parentbased_always_on`; также parentbased_always_off, always_on, always_off |
| `POSITION_CHECK_INTERVAL` | `30s`, период опроса и повторных запросов |
| `POSITION_STALE_AFTER` | `60s`, в целых микросекундах |
| `POSITION_WORKERS` | `4` |
| `POSITION_QUEUE_SIZE` | `100` ожидающих задач |
| `POSITION_SCAN_BATCH_SIZE` | `100` строк |
| `PUSH_HTTP_URL` | пусто; HTTP-адрес Push из `.env` ЛР3 |
| `PUSH_REQUEST_TIMEOUT` | `1s` |

Длительности и размеры очереди/пула/порции положительные, URL проверяются на
старте. Телеметрия идёт по OTLP в коллектор, затем в Prometheus/Grafana и Jaeger;
ссылки выводит `tripgoctl connect`. Недоступный коллектор не блокирует ready.

## HTTP и хранение

- `POST /api/v1/trips` → 201 с Location; `GET /api/v1/trips/{tripId}` и
  `POST /api/v1/trips/{tripId}/finish` → 200.
- `POST /api/v1/trips/{tripId}/positions` → 201 с точкой;
  `GET /api/v1/trips/{tripId}/positions` → 200 с `{"positions":[...]}`.
- Проверяются UUID, обязательные поля, координаты, RFC 3339 и `recorded_at`
  максимум на минуту в будущем. Неизвестные поля, null, лишний JSON и тело
  больше 1 MiB отклоняются. GET и finish требуют пустого тела.
- Ошибки: 400 invalid_request, 404 trip_not_found, 409 driver_busy/trip_completed,
  500 internal_error; формат `application/problem+json` без внутренних деталей.
- `/health` → 200; `/ready` → 200 или 503 после Ping с таймаутом.

Сохранение точки и обновление максимального `last_position_at` атомарны через
TxManager. Условный UPDATE активной поездки и finish блокируют одну строку:
завершённая поездка не примет точку из-за устаревшей проверки в Go. Опоздавшие
точки сохраняются, последнее время не уменьшается. Маршрут отсортирован по
`recorded_at, id`; пустой маршрут — массив `[]`, завершённый доступен для чтения.

Зависимости собираются в `internal/container`; интерфейсы объявлены у потребителей:

```text
cmd/trip-service           конфигурация, запуск, сигналы
internal/http             обработчики, валидация OpenAPI, Problem, health/ready
internal/usecase, domain   операции и правила поездок/координат
internal/repository       параметризованные SQL-запросы
internal/worker/positions  сканер, приоритетная очередь, пул
internal/gateway/push      HTTP-клиент Push Service
internal/infra            TxManager и OpenTelemetry
api, migrations           generated API и goose-миграции
```

## Опрос устаревших поездок

Сканер → ограниченная приоритетная очередь → фиксированные воркеры → Push Service.
Связка запускается после успешных Ping и открытия HTTP-порта.

Один сканер на каждом тике фиксирует время проверки и читает порции из БД.
SQL выбирает active по `COALESCE(last_position_at, started_at)`; продолжение —
по `(ReferenceAt, ID)`, без OFFSET. Partial индекс обслуживает и продолжение.
Сканер запоминает ID до конца тика: запоздалая точка может сдвинуть поездку
на следующую страницу, но второй Push за тот же обход не нужен.
Очередь на `container/heap` сохраняет этот приоритет между тиками. Дедупликация
охватывает ожидающие и выполняемые задачи. При заполнении более старая задача
вытесняет самую свежую; иначе отклоняется сама. Первый dropped завершает обход
с Warn, следующий тик начинает сначала. История отправок/cooldown не хранится.

После успешного commit координата удаляет ожидающую задачу. Перед отправкой
воркер проверяет в БД, что поездка ещё активна и её `ReferenceAt` совпадает
с выбранным сканером. Уже отправленный запрос отозвать нельзя. Гейтвей делает одну
попытку с собственным HTTP-клиентом и таймаутом; ретраев и rate limiter нет.

Метрики с префиксом `tripgo.`: `positions.saved`, `stale_checks{result}`,
`stale_checks.rows_read`, `position_requests.queue.size`,
`position_requests.workers.active`, `position_requests{result}` и
`position_requests.duration`. IDs не входят в метки. Span проверки содержит SQL;
отдельный root воркера связан с ним через span link, HTTP Push — дочерний спан
с W3C TraceContext. Логи JSON через slog коррелируются с трейсами.

SIGTERM: HTTP дожидается текущих запросов → сканер завершает SQL и останавливается
→ очередь закрывает приём и дренируется → SDK выгружает телеметрию при открытой
БД → закрываются Push-клиент и пул БД. Все шаги используют общий SHUTDOWN_TIMEOUT.
По исчерпании бюджета сканер и Push отменяются, остаток очереди отбрасывается,
приложение пишет явный лог и выходит с ненулевым кодом.

## Проверки

Нужен golangci-lint 2.14.0; остальные генераторы закреплены в go.mod.

```bash
golangci-lint fmt ./...
go mod tidy
make generate
make generate            # повтор не должен менять generated-файлы
go build ./...
go vet ./...
make test                # -race -count=2; coverage: bin/coverage.out
make lint                # обычная и integration-сборки
make test-integration    # Docker: отдельный PostgreSQL 17.6, -race -count=2
git diff --check
```

Unit-тесты используют gomock, управляемое время/тикер, in-memory OTel и goleak.
Интеграционные тесты проверяют rollback, гонки finish/координата, HTTP и фоновые
компоненты. Testcontainers применяет и откатывает миграции только на временной БД;
`.env` и данные tripgoctl не используются. Тест EXPLAIN загружает 9000 поездок,
делает ANALYZE и проверяет естественный index scan с продолжением. Его SQL,
параметры и планы сохраняются в игнорируемом `bin/lab03-evidence/explain/`.
