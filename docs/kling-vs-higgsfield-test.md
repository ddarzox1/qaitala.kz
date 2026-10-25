# A/B-тест Kling 3.0 и Higgsfield Genjutsu

Актуально на 25 октября 2026 года.

## Что именно сравниваем

Для сценария qaitala.kz нужен не обычный text-to-video, а перенос движения из
готового ролика на человека с фотографии.

Сравниваем три режима через уже используемый Higgsfield API:

| Вариант | Endpoint | Для чего |
|---|---|---|
| Kling 3.0 Motion Control Standard | `POST https://api.higgsfield.ai/kling-video/v3/motion-control/std` | один человек, дешёвый тест |
| Kling 3.0 Motion Control Pro | `POST https://api.higgsfield.ai/kling-video/v3/motion-control/pro` | один человек, максимальное качество Kling |
| Genjutsu Light Motion Transfer | `POST https://api.higgsfield.ai/higgsfield/genjutsu-light/motion-transfer/v1.0` | дешёвый вариант Higgsfield, первые 15 секунд |
| Genjutsu Motion Transfer | `POST https://api.higgsfield.ai/higgsfield/genjutsu/motion-transfer/v1.0` | полноценный дорогой режим для контрольного сравнения |

Авторизация у всех одинаковая:

```text
Authorization: Key ${HF_API_KEY_ID}:${HF_API_KEY_SECRET}
```

Статус любой задачи:

```text
GET https://api.higgsfield.ai/requests/{request_id}/status
```

На первом тесте не нужен отдельный ключ Kling. Прямой API Kling можно подключить
позже, если его тариф или лимиты окажутся выгоднее.

## Ограничения, важные для qaitala.kz

Kling Motion Control переносит движение **одного основного человека**. Ему нужны
одна фотография и один ролик. Лучший исходник: один человек целиком в кадре,
3–10 секунд, без монтажных склеек, резких движений и активной камеры. В режиме
`character_orientation: "video"` референс может быть до 30 секунд.

Genjutsu принимает несколько фотографий и лучше соответствует текущим шаблонам с
двумя и более ролями. Поэтому `hotel-lobby`, `delai`, `drake-party`, `lean`,
`raindance` и `3_meters` нельзя считать честным тестом Kling против Genjutsu:
Kling не решает ту же задачу.

Для первого сравнения:

1. Лучше всего снять отдельный 8–10-секундный ролик с одним человеком, без
   склеек, с видимыми лицом, руками и ногами.
2. Из имеющихся шаблонов первым попробовать `enzo`, но его 776×502 исходник
   слишком слабый для окончательного вывода.
3. Вторым попробовать `cadafi-football`: оценивать главного героя отдельно от
   сохранности остальных футболистов.

## Универсальные промпты

### Kling 3.0 Motion Control

```text
The person from the reference image performs the exact motion, gestures and facial expressions from the reference video. Preserve the person's facial identity, facial proportions, hairstyle, skin tone, body proportions and clothing from the reference image throughout the entire clip. Follow the reference video's action timing, body orientation, framing and camera motion. Keep one stable identity from the first frame to the last. Photorealistic, natural anatomy and realistic motion. No face morphing, identity drift, duplicate person, extra limbs, warped hands, clothing changes or flicker.
```

### Higgsfield Genjutsu / Genjutsu Light

```text
Replace only the primary performer in the source video with the person shown in reference image 1. Preserve the source video's exact motion, timing, camera path, framing, edit points, background, other people, props, lighting and original audio. Keep the reference person's facial identity, facial proportions, hairstyle, skin tone, body proportions and clothing consistent in every frame and angle. Do not alter, remove, duplicate or replace any other person or object. Photorealistic, natural anatomy and stable frame-to-frame detail. No face blending, identity drift, extra limbs, warped hands, clothing changes or flicker.
```

Промпты намеренно разные: Kling создаёт персонажа, который повторяет движение;
Genjutsu заменяет персонажа внутри исходной сцены. Одинаковый текст дал бы
нечестный и технически противоречивый тест.

## Промпты для имеющихся шаблонов

### `enzo`

Kling:

```text
The football player from the reference image performs the exact goal celebration from the reference video. Preserve the reference person's face, hairstyle, skin tone and athletic body proportions throughout the close-up. Match the original head turns, facial expression, timing, body orientation, framing and camera motion. Keep the identity stable during fast motion and profile angles. Photorealistic sports broadcast footage. No face morphing, identity drift, duplicate face, warped features or flicker.
```

Genjutsu:

```text
Replace only the celebrating football player's face and identity with the person in reference image 1. Preserve the original celebration, head turns, expression timing, body motion, football kit, stadium, broadcast camera, framing, other people and original audio. Keep the reference face recognizable and stable in front, profile and motion-blurred frames. Do not change any other part of the video. No blended face, identity drift, duplicate features, warped eyes or flicker.
```

### `cadafi-football`

Kling:

```text
The person from the reference image performs the exact football dribbling, footwork, turns and gestures of the primary performer in the reference video. Preserve the reference person's face, moustache, bald crown, curly side hair, body proportions, burgundy blazer, cream shirt, brown checked trousers and black boots with long upward-curled toes. Follow the source action timing, ball contacts, body orientation, framing and camera movement. Keep one stable identity and consistent boot shape throughout. Photorealistic stadium footage. No sunglasses, no headwear, no face morphing, clothing changes, extra limbs, duplicate person or flicker.
```

Genjutsu:

```text
Replace only the man in the long olive-green outfit, headwear and dark sunglasses who plays with the football with the person in reference image 1. Preserve the reference person's face, nose, jaw, moustache, bald crown, curly side hair and body proportions. Dress him in the burgundy blazer, cream shirt, brown checked trousers and black boots with long upward-curled toes shown in the reference. Completely remove the original man's outfit, headwear and sunglasses. Preserve the exact footwork, ball contacts, timing, ball trajectory, uniformed players, stadium, camera, editing and original audio. Do not alter the other players. Keep the identity and boot shape stable throughout. No face blending, distortions, duplicates, extra limbs or flicker.
```

## Готовые запросы

Перед запуском задайте публичные или временно подписанные URL:

```sh
export TEST_IMAGE_URL='https://.../person.jpg'
export TEST_VIDEO_URL='https://.../source.mp4'
```

### Kling Standard

```sh
curl --fail-with-body --silent --show-error \
  -X POST 'https://api.higgsfield.ai/kling-video/v3/motion-control/std' \
  -H "Authorization: Key ${HF_API_KEY_ID}:${HF_API_KEY_SECRET}" \
  -H 'Content-Type: application/json' \
  --data "$(jq -n \
    --arg image \"$TEST_IMAGE_URL\" \
    --arg video \"$TEST_VIDEO_URL\" \
    --arg prompt \"The person from the reference image performs the exact motion, gestures and facial expressions from the reference video. Preserve the person's facial identity, facial proportions, hairstyle, skin tone, body proportions and clothing throughout the entire clip. Follow the reference video's action timing, body orientation, framing and camera motion. Keep one stable identity. Photorealistic, natural anatomy and realistic motion. No face morphing, identity drift, duplicate person, extra limbs, warped hands, clothing changes or flicker.\" \
    '{prompt:$prompt,image_url:$image,video_url:$video,keep_original_sound:"yes",character_orientation:"video"}')"
```

Для Pro замените только endpoint на:

```text
https://api.higgsfield.ai/kling-video/v3/motion-control/pro
```

### Genjutsu Light

```sh
curl --fail-with-body --silent --show-error \
  -X POST 'https://api.higgsfield.ai/higgsfield/genjutsu-light/motion-transfer/v1.0' \
  -H "Authorization: Key ${HF_API_KEY_ID}:${HF_API_KEY_SECRET}" \
  -H 'Content-Type: application/json' \
  --data "$(jq -n \
    --arg image \"$TEST_IMAGE_URL\" \
    --arg video \"$TEST_VIDEO_URL\" \
    --arg prompt \"Replace only the primary performer in the source video with the person shown in reference image 1. Preserve the source video's exact motion, timing, camera path, framing, background, other people, props, lighting and original audio. Keep the reference person's facial identity, hairstyle, body proportions and clothing consistent in every frame. Do not alter any other person or object. Photorealistic, natural anatomy. No face blending, identity drift, extra limbs, warped hands, clothing changes or flicker.\" \
    '{prompt:$prompt,video_url:$video,image_urls:[$image],resolution:"720p"}')"
```

### Полный Genjutsu

Для полного Genjutsu замените endpoint на:

```text
https://api.higgsfield.ai/higgsfield/genjutsu/motion-transfer/v1.0
```

Сначала все варианты гоняются в 720p. Победителя повторить в максимальном
доступном качестве. Иначе деньги уйдут на сравнение разрешения, а не моделей.

## Ориентировочная себестоимость

Ниже промо-цены, показанные Higgsfield API на дату документа. Точный тариф нужно
проверить в аккаунте перед генерацией: скидки и правила биллинга меняются.

| Вариант | Ориентир | 10 секунд | 30 секунд |
|---|---:|---:|---:|
| Kling 3.0 Motion Standard | $0.063/с | $0.63 | $1.89 |
| Kling 3.0 Motion Pro | $0.084/с | $0.84 | $2.52 |
| Genjutsu Light 720p | примерно $0.10 за секунду исходника с учётом биллинга input + output | примерно $1.00 | не поддерживается, максимум 15 с |
| Genjutsu 720p | $0.3405/с | $3.41 | $10.22 |
| Genjutsu 1080p | $0.816/с | $8.16 | $24.48 |

При курсе 451.05 ₸/$ десятисекундный тест стоит примерно: Kling Standard 284 ₸,
Kling Pro 379 ₸, Genjutsu Light 451 ₸, Genjutsu 720p 1 536 ₸, Genjutsu 1080p
3 681 ₸. Банковская конвертация и налоги сверху не учтены.

## Протокол оценки

Для каждой модели сделать **два запуска** на одинаковых входах. Один запуск
слишком зависит от случайности генерации.

Оценивать вслепую по шкале 1–5:

| Критерий | Вес |
|---|---:|
| Узнаваемость лица и сохранение личности | 30% |
| Точность движения и тайминга | 20% |
| Стабильность лица между кадрами | 15% |
| Руки, ноги и анатомия | 10% |
| Сохранность сцены, людей, предметов и мяча | 10% |
| Одежда и детали персонажа | 5% |
| Артефакты и мерцание | 5% |
| Время генерации | 3% |
| Себестоимость | 2% |

Фиксировать также: `request_id`, endpoint, фактическую цену, время до результата,
разрешение, номер запуска и ссылку на результат. Победителя выбирать по среднему
взвешенному баллу двух запусков, а не по одному удачному ролику.

## Источники

- Kling Motion Control через Higgsfield: <https://open.higgsfield.ai/models/kling-video/v3/motion-control/std/api-reference>
- Kling Motion Control Pro: <https://open.higgsfield.ai/models/kling-video/v3/motion-control/pro/playground>
- Genjutsu: <https://open.higgsfield.ai/models/higgsfield/genjutsu/motion-transfer/v1.0/playground>
- Genjutsu Light: <https://open.higgsfield.ai/models/higgsfield/genjutsu-light/motion-transfer/v1.0/playground>
- Тарифы Higgsfield API: <https://open.higgsfield.ai/pricing>
- Прямой API Kling: <https://kling.ai/document-api/api/video/motion-control>
- Рекомендации Kling для исходников: <https://kling.ai/quickstart/motion-control-user-guide>
