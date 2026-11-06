# TripGo — Trip Service

Сервис создаёт, возвращает и завершает поездки, сохраняя историю статусов
в PostgreSQL. Контракт: [OpenAPI](contracts/openapi/trip-service.openapi.yaml).

## Запуск

Нужны Go 1.26.2 или новее, Make, Docker и
[tripgoctl](https://github.com/course-go-autumn-2026/course-infra#установка).
Команды выполняются из корня репозитория:

```bash
tripgoctl cluster start
tripgoctl environment start
tripgoctl connect
make migrate
make run
```

`make run` собирает `bin/trip-service` и запускает его с `-local-env`.
Настройки читаются с приоритетом: окружение процесса → `.env` → `.env.example`.
Dotenv-файлы не исполняются как shell-код. Без `-local-env` используется только
окружение процесса. `.env` создаёт tripgoctl; не заменяйте его примером.
`config/trip-service.env.example` сервис не загружает.

Все настройки обязательны. Конфигурация проверяется до подключения к БД:

| Переменная | Назначение |
|---|---|
| `HTTP_ADDR` | Адрес host:port, порт 1..65535 |
| `LOG_LEVEL` | debug, info, warn или error |
| `SHUTDOWN_TIMEOUT` | Общий бюджет остановки HTTP, SDK телеметрии и пула |
| `HTTP_READ_TIMEOUT` | Чтение HTTP-запроса |
| `HTTP_READ_HEADER_TIMEOUT` | Чтение заголовков, не больше READ_TIMEOUT |
| `HTTP_WRITE_TIMEOUT` | Запись HTTP-ответа |
| `HTTP_IDLE_TIMEOUT` | Ожидание следующего запроса |
| `DATABASE_URL` | URL PostgreSQL с пользователем, хостом и именем БД |
| `DATABASE_MAX_CONNS` | Максимум соединений, 1..2147483647 |
| `DATABASE_MIN_CONNS` | Минимум соединений, 0..MAX_CONNS |
| `DATABASE_MAX_CONN_LIFETIME` | Срок жизни соединения, больше CONNECT_TIMEOUT |
| `DATABASE_CONNECT_TIMEOUT` | Подключение и стартовый Ping |
| `DATABASE_QUERY_TIMEOUT` | Бюджет транзакции, запроса и ready, меньше WRITE_TIMEOUT |

Длительности задаются положительными Go duration, например `3s`.
Миграции при старте автоматически не применяются. `make migrate-status`
показывает их состояние, `make migrate-down` откатывает одну миграцию,
удаляя соответствующую таблицу с данными. Откаты проверяйте на отдельной БД.

## Архитектура

```text
cmd/trip-service          конфигурация, container.New(ctx, ...), запуск и сигналы
internal/config           разбор и проверка окружения
internal/container        сборка зависимостей, стартовый Ping, HTTP-сервер и shutdown
internal/http             маршрутизатор, OpenAPI-валидация и делегирующий адаптер
  trip/create, get, finish обработчики с узкими интерфейсами usecase
  trip                    общее преобразование поездки в ответ
  domainerror              единый маппинг доменных ошибок в Problem
  response, health         запись ответов и проверки доступности
internal/usecase/trip      create, get, finish; интерфейсы репозитория и транзакций
internal/domain/trip       модель, валидация, статусы, Finish и доменные ошибки
internal/model             вход создания и запись истории
internal/repository/trip   параметризованные SQL-запросы
internal/infra/postgres/transaction  транзакции через context
internal/infra/telemetry    OTel SDK, OTLP, otelpgx и бизнес-метрики
internal/logging           корреляция slog со спаном и безопасные причины ошибок
api                       сгенерированный код OpenAPI
```

Конкретные зависимости собираются только в container: пул → транзакции и
репозиторий → usecase → обработчики → маршрутизатор → сервер. HTTP не знает
про PostgreSQL; ready получает интерфейс `Ping(context.Context) error`.
Интерфейсы объявляет потребитель в `contract.go`, зависимости передаются
конструкторами. Домен не зависит от HTTP и инфраструктуры.

Пути `cmd/trip-service` и `api/` сохранены по conventions.md: это допустимые
имена из ЛР1, их переименование не меняет архитектуру.
UUID проверяются шаблонами в отдельной копии схемы каждого роутера;
глобальной регистрации валидаторов и `init()` в коде сервиса нет.

HTTP выполняется в порядке Recover → Tracing → Logging → Metrics → роутер.
Один серверный спан создаёт otelhttp, SQL инструментируется otelpgx на уровне
пула. В HTTP-именах и метках используются шаблоны маршрутов, для неизвестных
путей — `/unmatched`. Секреты, SQL-параметры и тексты внутренних ошибок
в ответы и телеметрию не попадают; диагностика сохраняет операцию и SQLSTATE.

## Телеметрия

Переменные ЛР2 перечислены в `.env.example`:

| Переменная | Назначение |
|---|---|
| `OTEL_SERVICE_NAME` | Имя сервиса, обычно trip-service |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Обязательный HTTP(S) URL коллектора из окружения ЛР2 |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | http/protobuf или grpc |
| `OTEL_RESOURCE_ATTRIBUTES` | service.namespace=tripgo,deployment.environment=local |
| `OTEL_TRACES_SAMPLER` | parentbased_always_on, parentbased_always_off, always_on или always_off |

Ресурс включает service.name, service.namespace, deployment.environment и
service.version из информации о сборке; версию можно задать в
OTEL_RESOURCE_ATTRIBUTES. Адрес коллектора в коде отсутствует. Экспорт
асинхронный: недоступный коллектор не блокирует запросы и ready. SDK выгружает
данные после остановки HTTP, пока пул ещё открыт, в общем SHUTDOWN_TIMEOUT.

Метрики: http.server.request.duration, http.server.active_requests,
db.client.operation.duration, tripgo.trips.created, tripgo.trips.completed
и tripgo.trips.active. otelhttp также отдаёт размеры тел запросов и ответов.
HTTP-длительность не дублируется. Для DB-длительности метки каталога добавляет
pool tracer; встроенный meter otelpgx отключён, спаны остаются библиотечными.
Бизнес-счётчики и логи обновляются после commit. Gauge читает реальный COUNT
активных поездок с таймаутом, без подстановки нуля при ошибке.

Создание и завершение вместе с историей выполняются атомарно. Транзакция
передаётся через context; вложенный Do использует её же, вне транзакции
репозиторий работает через пул. Завершение вызывает доменный Finish,
затем условный UPDATE по status = active и только после успеха пишет историю.
Уникальный индекс защищает водителя от двух активных поездок.
При SIGINT/SIGTERM сервер прекращает принимать запросы, дожидается текущих,
затем закрывается пул — всё в пределах SHUTDOWN_TIMEOUT.

## HTTP

- `POST /api/v1/trips` → 201 с Location; `GET /api/v1/trips/{id}` и
  `POST /api/v1/trips/{id}/finish` → 200.
- Обязательны ненулевые UUID пользователя и водителя, обе точки и цена int64 ≥ 0.
  Широта −90..90, долгота −180..180; нулевые значения допустимы.
- Проверяются обязательные поля, null, неизвестные свойства, EOF и лимит тела
  1 MiB. GET и finish требуют пустого тела.
- Ошибки: 400 invalid_request, 404 trip_not_found, 409 driver_busy/trip_completed,
  500 internal_error. Формат — application/problem+json без технических деталей.
- `/health` → 200 без БД; `/ready` → 200 или 503 после Ping с таймаутом.
- Необязательные finished_at и last_position_at отсутствуют до заполнения.
  Операции с координатами из будущих работ пока не подключены.

## Проверки

```bash
make generate       # OpenAPI и gomock через инструменты go.mod
make lint           # обычный код и код с тегом integration, без запуска тестов
make build
make test           # все unit-тесты с -race -count=2
make test-integration # PostgreSQL через testcontainers, -race -count=2
# профиль domain/usecase: bin/coverage.out; итог выводит go tool cover

go mod tidy
go build ./...
go vet ./...
git diff --check
```

Для `make lint` используется golangci-lint **2.14.0**. Конфигурация основана
на [конфиге курса](https://github.com/course-go-autumn-2026/course/blob/main/homework/config/golangci.yml)
с путями текущего модуля и generated API в `api/`. Форматирование gofumpt/gci:
`golangci-lint fmt ./...`.

Моки генерируются `go:generate` и `go tool mockgen` рядом с контрактами
в тестовых пакетах. Тесты проверяют домен, usecase, HTTP, OpenAPI-валидацию,
ошибки и health/ready без настоящей БД. Обычный `make test` не требует Docker.

Для `make test-integration` нужен запущенный Docker. Testcontainers создаёт
один временный изолированный PostgreSQL `17.6` на запуск пакета и применяет
миграции через goose. Первая загрузка образа требует сети. `.env`, DATABASE_URL
сервиса и данные tripgoctl не используются. При недоступном Docker команда
завершается ошибкой. Каждый тест удаляет только собственные данные; после
запуска закрывается пул и удаляется тестовый контейнер.

Интеграционные сценарии проверяют репозиторий, usecase, rollback обеих таблиц
и конкурентные создание и завершение. Они отделены тегом `integration`.
Unit-тесты callback сами по себе не подтверждают настоящий rollback.

Тесты SDK используют InMemoryExporter и ManualReader; интеграционный тест
проверяет связку HTTP → SQL и метрики на временной PostgreSQL. Живой экспорт
в окружении ЛР2 проверен в Jaeger/Grafana; скриншоты подготовлены для PR.
Gremlins v0.6.0 дал для domain/usecase 44 варианта: 31 пойман тестами,
13 не скомпилировались, выживших исполнимых — 0.
