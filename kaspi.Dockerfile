# В репозитории tapter-dev/kaspi-pos-automation своего Dockerfile нет.
# Коммит закреплён намеренно: это платёжный слой, молча обновляться он не должен.
# Kaspi периодически поднимает минимальную версию приложения — тогда вход
# падает с OldVersionToUpdate. Лечится переменными APP_VERSION/APP_BUILD
# у сервиса kaspi, пересобирать ради этого не нужно.
FROM node:20-alpine
ARG KASPI_REF=175577ee1b34437dba46fb9bb0c8706f57f3b311
RUN apk add --no-cache git \
 && git clone https://github.com/tapter-dev/kaspi-pos-automation.git /app \
 && cd /app && git checkout "$KASPI_REF" \
 && npm ci --omit=dev \
 && apk del git
WORKDIR /app
# Сервис пишет состояние в корень /app, а не в подпапку. Без этих ссылок
# пересоздание контейнера сменит ключи устройства (сессия Kaspi умрёт)
# и потеряет отслеживаемые платежи — вебхук об оплате не придёт.
RUN mkdir data && for f in keypair.json ecdh-keypair.json device.json webhooks.json \
      tracked-payments.json webhook-retries.json last-session.json; do \
      ln -s "data/$f" "$f"; done
VOLUME ["/app/data"]
EXPOSE 3000
CMD ["node", "server.js"]
