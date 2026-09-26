# Корневой сертификат Минцифры

`rootca_ssl_rsa2022.crt` — Russian Trusted Root CA (Минцифры России), PEM.

- Зачем: сертификат `platform-api2.max.ru` выдан Russian Trusted Sub CA. Без этого корня в хранилище доверенных сертификатов запросы бота к MAX Bot API не проходят проверку TLS. Проверку TLS мы не отключаем.
- Источник: https://gu-st.ru/content/lending/russian_trusted_root_ca_pem.crt (раздел Госуслуг о сертификатах Минцифры).
- SHA-256 отпечаток: `D2:6D:2D:02:31:B7:C3:9F:92:CC:73:85:12:BA:54:10:35:19:E4:40:5D:68:B5:BD:70:3E:97:88:CA:8E:CF:31`, действует до 27.02.2032.
- Где используется: копируется в образ `app` и добавляется через `update-ca-certificates` (`backend/Dockerfile`); при запуске без Docker — переменная `EXTRA_CA_FILE`.
