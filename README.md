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

## Тесты

```bash
cd code
make test
```

## Пример вывода

```json
{
  "root_url": "https://example.com",
  "depth": 1,
  "generated_at": "2026-04-07T12:00:00Z",
  "pages": [
    {
      "url": "https://example.com",
      "depth": 0,
      "http_status": 200,
      "status": "ok",
      "seo": {
        "has_title": true,
        "title": "Example Domain",
        "has_description": false,
        "description": "",
        "has_h1": true
      },
      "broken_links": [],
      "assets": [],
      "discovered_at": "2026-04-07T12:00:00Z"
    }
  ]
}
```
