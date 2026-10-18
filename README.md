# qaitala.kz

Клиент выбирает шаблон-ролик, загружает свои фото и платит через Kaspi QR.
Higgsfield Genjutsu подставляет его в ролик, готовое видео доступно по ссылке
`/o/{token}`. Аккаунтов нет: токен в ссылке и есть доступ.

Стек: Go 1.26, Postgres, S3-совместимое хранилище (в бою — Cloudflare R2), ffmpeg.
Фронтенд — один `index.html`, вшит в бинарник вместе с миграциями.

## Локальный запуск без трат

Нужны Go 1.26, Docker и ffmpeg (конвертирует HEIC с айфонов и режет ролики в `seedclips`).

1. Поднять Postgres (порт 5434) и S3 на SeaweedFS (порт 8333):

   ```sh
   docker compose -f docker-compose.dev.yml up -d
   ```

2. Создать `.env` в корне:

   ```sh
   PORT=8099
   PUBLIC_BASE_URL=http://localhost:8099
   DATABASE_URL=postgres://qaitala:qaitala@localhost:5434/qaitala?sslmode=disable

   S3_ENDPOINT=http://localhost:8333
   S3_REGION=us-east-1
   S3_BUCKET=qaitala
   S3_KEY=dev
   S3_SECRET=dev
   S3_PUBLIC_BASE=http://localhost:8333/qaitala

   FAKE_PAYMENTS=true
   FAKE_VIDEO=true
   ```

   С `FAKE_*` ключи Kaspi и Higgsfield не нужны.

3. Если бакета ещё нет:

   ```sh
   aws --endpoint-url http://localhost:8333 s3 mb s3://qaitala
   ```

4. Залить шаблоны. Положить видео в `clips/<id>/video.mp4` (в git их нет) и
   проставить цены в `meta.json`: без цены шаблон в каталог не попадает.
   Формат папок описан в [clips/README.md](clips/README.md).

   ```sh
   go run ./cmd/seedclips            # все шаблоны
   go run ./cmd/seedclips -only enzo # один
   ```

5. Запустить сервер. Миграции накатываются сами при старте.

   ```sh
   go run ./cmd/server
   ```

   Открыть http://localhost:8099.

Что делают заглушки:
- `FAKE_PAYMENTS` — вместо Kaspi ссылка `/fake-pay/{token}`: открыл её, и заказ считается оплаченным.
- `FAKE_VIDEO` — Higgsfield не вызывается, через ~6 секунд результатом становится сам исходный ролик.

## Продакшен (docker compose)

Стек из `docker-compose.yml`:

| сервис     | что это |
|------------|---------|
| `api`      | этот сервер, порт 8080 |
| `kaspi`    | [kaspi-pos-automation](https://github.com/tapter-dev/kaspi-pos-automation), собирается из `kaspi.Dockerfile`. Наружу не открыт, к нему ходит только `api` |
| `postgres` | база, данные в томе `pg_data` |

### 1. Переменные

```sh
cp .env.example .env
```

Что где взять — в комментариях `.env.example`. Коротко:

| переменная | откуда |
|---|---|
| `PUBLIC_BASE_URL` | внешний адрес сайта, обязательно https: на него приходят вебхуки Higgsfield |
| `POSTGRES_PASSWORD` | любой надёжный пароль |
| `KASPI_TOKEN_SECRET_KEY` | `openssl rand -hex 32` |
| `KASPI_TOKEN_SN`, `KASPI_VTOKEN_SECRET`, `KASPI_PROFILE_ID` | после SMS-авторизации в kaspi-сервисе (шаг 3) |
| `KASPI_WEBHOOK_SECRET` | случайная строка, та же, что в `webhooks.json` kaspi-сервиса |
| `HF_API_KEY_ID`, `HF_API_KEY_SECRET` | console.higgsfield.ai |
| `HF_WEBHOOK_SECRET` | случайная строка |
| `S3_*` | Cloudflare R2: эндпоинт аккаунта, токен Object Read & Write на корзину, публичный домен |
| `WORKER_CONCURRENCY` | не выше лимита аккаунта Higgsfield, иначе он отвечает 400 |

> **Хранилище.** В R2 публична вся корзина целиком. Фото клиентов и готовые
> ролики (`orders/*`) закрывает только WAF-правило на домене `S3_PUBLIC_BASE`,
> которое пропускает лишь `/public/`. Правило приведено в `.env.example`.
> Без него фото клиентов открыты.

### 2. Запуск

```sh
docker compose up -d --build
```

### 3. Kaspi: SMS-авторизация (один раз и при протухании сессии)

У kaspi-сервиса есть веб-интерфейс, но порт наружу не открыт намеренно.
Откройте его временно и только на localhost сервера:

```sh
docker compose stop kaspi
docker compose run --rm -p 127.0.0.1:3000:3000 kaspi
# на своей машине:
ssh -L 3000:127.0.0.1:3000 <сервер>
```

Дальше:
1. Откройте http://localhost:3000, введите номер кассира Kaspi Pay и код из SMS.
2. Перенесите `tokenSN`, `vtokenSecret` и `profileId` в `.env`
   (`KASPI_TOKEN_SN`, `KASPI_VTOKEN_SECRET`, `KASPI_PROFILE_ID`).
3. Остановите временный контейнер.

`vtokenSecret` зашифрован ключом `KASPI_TOKEN_SECRET_KEY`: смените ключ — придётся
авторизоваться заново. Ключи устройства лежат в томе `kaspi_data`. Удалите том — Kaspi
увидит новое устройство, и авторизацию тоже придётся пройти заново.

Один раз создайте `webhooks.json`, куда сервис шлёт уведомления об оплате (секрет — `KASPI_WEBHOOK_SECRET`):

```sh
docker compose run --rm -T --entrypoint sh kaspi -c 'cat > data/webhooks.json' <<'EOF'
[{"url": "http://api:8080/webhooks/kaspi", "events": ["payment.success"], "secret": "<KASPI_WEBHOOK_SECRET>"}]
EOF
```

Затем:

```sh
docker compose up -d
```

### 4. Шаблоны

Папка `clips/` в образ не попадает (`.dockerignore`), поэтому её монтируют:

```sh
docker compose run --rm -v ./clips:/app/clips api ./seedclips
```

Команда идемпотентна: поправили видео или `meta.json` — запустите снова.

## Эксплуатация

Что искать в логах `api`:
- `СЕССИЯ KASPI НЕДЕЙСТВИТЕЛЬНА` — оплата не работает. Повторите шаг 3.
- `ЗАПРОШЕН ВОЗВРАТ` — клиент запросил возврат после неудачной генерации. Деньги возвращаются вручную.
- `СУММА НЕ СОВПАЛА` — пришла оплата не на ту сумму, генерация не запущена. Разберитесь вручную.

Брошенные заказы закрываются сами через сутки (`expired`). Зависшие генерации
перепроверяются раз в минуту.

## Тесты

```sh
go test ./...
```
