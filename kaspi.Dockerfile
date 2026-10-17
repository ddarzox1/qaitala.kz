# В репозитории tapter-dev/kaspi-pos-automation своего Dockerfile нет.
# Коммит закреплён намеренно: это платёжный слой, молча обновляться он не должен.
FROM node:20-alpine
ARG KASPI_REF=main
RUN apk add --no-cache git \
 && git clone https://github.com/tapter-dev/kaspi-pos-automation.git /app \
 && cd /app && git checkout "$KASPI_REF" \
 && npm ci --omit=dev \
 && apk del git
WORKDIR /app
# keypair.json, device.json и webhooks.json переживают пересоздание контейнера
VOLUME ["/app/data"]
EXPOSE 3000
CMD ["node", "server.js"]
