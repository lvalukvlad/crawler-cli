# crawler-cli

Сборка:

```bash
go test ./...
go build -o crawler-cli .
```

Запуск:

```bash
./crawler-cli \
  --urls https://example.com \
  --depth 2 \
  --timeout 1m \
  --request-timeout 10s \
  --output result.json \
  --log crawler.log
```

Несколько URL — через запятую. `--depth 0` качает только стартовые страницы.

Ctrl+C останавливает обход. Результат — `result.json`.
