### Hexlet tests and linter status:
[![Actions Status](https://github.com/krenar-rm/go-project-316/actions/workflows/hexlet-check.yml/badge.svg)](https://github.com/krenar-rm/go-project-316/actions)

# Hexlet Go Crawler

Утилита для анализа структуры веб-сайтов. Проверяет SEO теги, находит битые ссылки, анализирует ассеты.

## Установка и запуск

```bash
cd code
make build
```

## Использование

```bash
make run URL=https://example.com
```

Или напрямую:

```bash
go run ./cmd/hexlet-go-crawler https://example.com
```

### Флаги

- `--depth` — глубина обхода (по умолчанию 10)
- `--retries` — количество повторных попыток (по умолчанию 1)
- `--delay` — задержка между запросами (пример: 200ms)
- `--timeout` — таймаут запроса (по умолчанию 15s)
- `--rps` — ограничение запросов в секунду (приоритетнее delay)
- `--user-agent` — кастомный User-Agent
- `--workers` — количество воркеров (по умолчанию 4)

## Глубина обхода

Параметр `--depth` задает максимальное количество переходов по ссылкам от стартовой страницы. Стартовая страница имеет depth=0, её ссылки — depth=1 и т.д. По умолчанию глубина 10.

Например `--depth 1` обходит только стартовую страницу, `--depth 2` — стартовую и все страницы, на которые она ссылается. Обходятся только страницы внутри исходного домена, внешние ссылки проверяются но не краулятся.

## Тесты

```bash
cd code
make test
```

## Формат JSON-отчета

```json
{
  "root_url": "https://example.com",
  "depth": 1,
  "generated_at": "2024-06-01T12:34:56Z",
  "pages": [
    {
      "url": "https://example.com",
      "depth": 0,
      "http_status": 200,
      "status": "ok",
      "error": "",
      "seo": {
        "has_title": true,
        "title": "Example title",
        "has_description": true,
        "description": "Example description",
        "has_h1": true
      },
      "broken_links": [
        {
          "url": "https://example.com/missing",
          "status_code": 404,
          "error": "Not Found"
        }
      ],
      "assets": [
        {
          "url": "https://example.com/static/logo.png",
          "type": "image",
          "status_code": 200,
          "size_bytes": 12345,
          "error": ""
        }
      ],
      "discovered_at": "2024-06-01T12:34:56Z"
    }
  ]
}
```

### Описание полей

- `root_url` — стартовый URL обхода
- `depth` — заданная глубина обхода
- `generated_at` — время генерации отчета (ISO 8601)
- `pages` — массив обойденных страниц
  - `url` — адрес страницы
  - `depth` — расстояние от стартового URL (0 = корень)
  - `http_status` — HTTP статус ответа
  - `status` — "ok" или "error"
  - `error` — текст ошибки (пустая строка если нет)
  - `seo` — SEO информация (title, description, h1)
  - `broken_links` — ссылки с ошибками (4xx/5xx/сетевые)
  - `assets` — статические ресурсы (image, script, style)
  - `discovered_at` — время обнаружения страницы (ISO 8601)

Флаг `IndentJSON` влияет только на форматирование, содержание не меняется.
