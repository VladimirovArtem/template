# TripGo — Trip Service

Сервис создаёт, возвращает и завершает поездки. В лабораторной №1 реализованы
HTTP API, конфигурация, PostgreSQL, миграции, история статусов, менеджер
транзакций и корректная остановка сервиса.

## Требования и запуск

Нужны Go 1.26.2, Make, запущенный Docker и `tripgoctl`. Для примеров ниже нужны
curl и jq. При первой сборке нужен доступ к Go-модулям. Версии зависимостей,
`goose` и `oapi-codegen` закреплены в `go.mod` и `go.sum`.

Установите `tripgoctl` по [инструкции курса](https://github.com/course-go-autumn-2026/course-infra#установка).
Затем из корня проекта, где уже есть `environment.toml`, выполните:

```bash
tripgoctl cluster start
tripgoctl environment start
tripgoctl connect
make migrate
make migrate-status
make run
```

`tripgoctl environment start` создаёт `.env` с адресом PostgreSQL.
**Не заменяйте `.env` копией `.env.example`: пример не содержит адреса и пароля БД.**
Если окружение уже запущено, достаточно `make migrate`, затем `make run`.
Миграции автоматически при старте сервиса не применяются.

`make run` собирает `bin/trip-service` и запускает его с `-local-env`.
Сервис и команды миграций загружают настройки с приоритетом:
**окружение процесса → `.env` → `.env.example`**. Dotenv-файлы не исполняются
как shell-код. Явно пустая переменная тоже имеет приоритет и проходит валидацию.
Без `-local-env` бинарник читает только окружение процесса.

```bash
HTTP_ADDR=127.0.0.1:8081 LOG_LEVEL=debug make run
```

## Конфигурация

Все 13 переменных обязательны для сервиса. При `make run` значения, кроме
`DATABASE_URL`, берутся из корневого [.env.example](.env.example).
Исходный `config/trip-service.env.example` не загружается.

| Переменная | Значение в примере | Назначение |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Адрес HTTP-сервера |
| `LOG_LEVEL` | `info` | Уровень JSON-логов: `debug`, `info`, `warn`, `error` |
| `SHUTDOWN_TIMEOUT` | `10s` | Общий бюджет остановки HTTP и пула |
| `HTTP_READ_TIMEOUT` | `10s` | Чтение всего запроса |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | Чтение заголовков |
| `HTTP_WRITE_TIMEOUT` | `10s` | Запись ответа |
| `HTTP_IDLE_TIMEOUT` | `60s` | Простой keep-alive соединения |
| `DATABASE_URL` | пусто | URL PostgreSQL, который подготавливает tripgoctl |
| `DATABASE_MAX_CONNS` | `10` | Максимальное число соединений |
| `DATABASE_MIN_CONNS` | `2` | Минимальное число соединений |
| `DATABASE_MAX_CONN_LIFETIME` | `30m` | Максимальное время жизни соединения |
| `DATABASE_CONNECT_TIMEOUT` | `5s` | Подключение и стартовый Ping |
| `DATABASE_QUERY_TIMEOUT` | `3s` | Бюджет `/ready`, транзакции или отдельного запроса, включая ожидание пула |

Настройки проверяются до подключения к БД:

- Длительности имеют формат Go (`500ms`, `5s`, `30m`) и больше нуля.
- `HTTP_READ_HEADER_TIMEOUT <= HTTP_READ_TIMEOUT`;
  `DATABASE_QUERY_TIMEOUT < HTTP_WRITE_TIMEOUT`;
  `DATABASE_MAX_CONN_LIFETIME > DATABASE_CONNECT_TIMEOUT`.
- `1 <= DATABASE_MAX_CONNS <= 2147483647`,
  `0 <= DATABASE_MIN_CONNS <= DATABASE_MAX_CONNS`.
- `HTTP_ADDR` имеет формат `host:port`; порт — от 1 до 65535.
- `DATABASE_URL` — `postgres://` или `postgresql://` с пользователем, одним
  TCP-хостом и именем БД. Явный порт — от 1 до 65535. Unix-сокеты и списки
  хостов не поддерживаются. Отдельные настройки пула и подключения важнее опций URL.

Ошибка конфигурации называет переменную, не раскрывая её значение.
Недоступная БД или занятый HTTP-порт приводят к ненулевому коду выхода.

## Сборка, генерация и миграции

Все команды выполняются из корня проекта:

```bash
make generate        # обновить api/api.gen.go из OpenAPI
make build           # собрать bin/trip-service
make migrate         # применить ожидающие миграции
make migrate-status  # показать состояние миграций
make migrate-down    # откатить одну последнюю миграцию
```

Генерация использует [OpenAPI-контракт](contracts/openapi/trip-service.openapi.yaml)
и [конфигурацию](config/oapi-codegen.yaml): модели, chi-сервер и встроенную схему
для пяти операций этой работы. Инструменты запускаются через `go tool`;
отдельная глобальная установка не нужна. Сгенерированный код вручную не меняется.

Две миграции создают `trips` и `trip_status_history` транзакционно.
Повторный `make migrate` пропускает уже применённые версии.
**`make migrate-down` откатывает одну миграцию и удаляет соответствующую таблицу
с данными:** сначала историю, при следующем вызове — поездки.
Для проверки откатов используйте отдельную тестовую БД.
Роли БД нужны права на миграции, чтение и запись таблиц и последовательность истории.

## HTTP API

После запуска откройте второй терминал. Пример для адреса `:8080`:

```bash
curl -i http://localhost:8080/health
curl -i http://localhost:8080/ready

trip_json=$(curl --fail-with-body -sS http://localhost:8080/api/v1/trips \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"5cb72c04-7650-45c9-a79b-bcdba0631e0c",
       "driver_id":"8860b315-ec86-42eb-a17c-7c163d721ff5",
       "start_point":{"latitude":59.9398,"longitude":30.3146},
       "end_point":{"latitude":59.929,"longitude":30.3626},
       "price":1450}')
printf '%s\n' "$trip_json" | jq
TRIP_ID=$(printf '%s' "$trip_json" | jq -r .id)
curl -i "http://localhost:8080/api/v1/trips/$TRIP_ID"
curl -i -X POST "http://localhost:8080/api/v1/trips/$TRIP_ID/finish"
curl -i "http://localhost:8080/api/v1/trips/$TRIP_ID"
```

Создание возвращает `201`, поездку со статусом `active` и заголовок
`Location: /api/v1/trips/{id}`. Получение и завершение возвращают `200`.
После завершения статус — `completed`, поле `finished_at` содержит время RFC 3339.
Успешные ответы имеют тип `application/json`.

Обязательны ненулевые UUID пользователя и водителя, обе точки со всеми
координатами и целая цена `int64 >= 0`. Широта — от −90 до 90, долгота — от −180
до 180. Нулевые цена и координаты допустимы. Отсутствующие поля, `null`,
неизвестные поля и лишний JSON отклоняются. Лимит тела — 1 MiB;
GET и finish требуют пустого тела.

Ошибки возвращаются как `application/problem+json` с полями `type`, `title`,
`status`, `detail`, `instance`, `code`, без внутренних ошибок БД и секретов:

| HTTP | `code` | Причина |
|---|---|---|
| 400 | `invalid_request` | Невалидное тело или UUID |
| 404 | `trip_not_found` | Поездки нет |
| 409 | `driver_busy` | У водителя уже есть активная поездка |
| 409 | `trip_completed` | Поездка уже завершена |
| 500 | `internal_error` | Внутренняя ошибка |

`/health` возвращает `200 {"status":"ok"}` без обращения к БД.
`/ready` проверяет БД с таймаутом: `200 {"status":"ok"}` или
`503 {"status":"unavailable"}`. Наличие таблиц эта проверка не подтверждает.

`finished_at` до завершения и `last_position_at` отсутствуют в JSON из-за
`omitempty`. Эти nullable-поля не входят в `required`, поэтому их отсутствие
допустимо, хотя пример задания показывает `null`. Колонки `last_position_at`
в БД этой работы нет.

## Решения

Используется уровень изоляции Read Committed. Для наших операций его
достаточно: уникальный индекс защищает от создания двух активных поездок,
а завершение выполняется через UPDATE с условием status = 'active'.
При одновременном завершении одна операция проходит, другая получает
ошибку trip_completed.

Менеджер транзакций открывает транзакцию и передаёт её через context.
Репозиторий использует транзакцию из контекста, а если её нет — пул.
Вложенный Do использует ту же транзакцию. При успехе выполняется commit,
при ошибке или панике — rollback; паника передаётся дальше.
Изменение поездки и запись истории выполняются в одной транзакции.

Частичный уникальный индекс по driver_id для строк со статусом active
не позволяет водителю иметь две активные поездки, в том числе при
одновременных запросах. Нарушение этого ограничения возвращается клиенту
как 409 driver_busy.

## Проверки и ограничения

`make test` запускает `go test -race ./...`.

Бонусные задания не реализованы.
