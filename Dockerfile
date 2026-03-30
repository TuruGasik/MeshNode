FROM eclipse-mosquitto:2

# Install envsubst for runtime config templating
RUN if command -v apk >/dev/null 2>&1; then \
            apk add --no-cache gettext; \
        elif command -v apt-get >/dev/null 2>&1; then \
            apt-get update && apt-get install -y --no-install-recommends gettext-base && rm -rf /var/lib/apt/lists/*; \
        else \
            echo "Unsupported base image: cannot install envsubst" && exit 1; \
        fi

# Copy custom configuration
COPY mosquitto/config/mosquitto.conf /mosquitto/config/mosquitto.conf
COPY scripts/start-mosquitto.sh /usr/local/bin/start-mosquitto.sh

# Ensure data and log directories exist with correct permissions
RUN mkdir -p /mosquitto/data /mosquitto/log \
        && chown -R mosquitto:mosquitto /mosquitto \
        && chmod +x /usr/local/bin/start-mosquitto.sh

EXPOSE 1883

CMD ["/usr/local/bin/start-mosquitto.sh"]
