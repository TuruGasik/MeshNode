FROM eclipse-mosquitto:2

# Copy custom configuration
COPY mosquitto/config/mosquitto.conf /mosquitto/config/mosquitto.conf

# Ensure data and log directories exist with correct permissions
RUN mkdir -p /mosquitto/data /mosquitto/log \
    && chown -R mosquitto:mosquitto /mosquitto

EXPOSE 1883

CMD ["/usr/sbin/mosquitto", "-c", "/mosquitto/config/mosquitto.conf"]
