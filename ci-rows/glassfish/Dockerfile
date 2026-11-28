FROM eclipse-temurin:21-jre
RUN apt-get update && apt-get install -y --no-install-recommends unzip && rm -rf /var/lib/apt/lists/*
ADD https://download.eclipse.org/ee4j/glassfish/glassfish-7.0.25.zip /tmp/glassfish.zip
RUN unzip -q /tmp/glassfish.zip -d /opt && rm /tmp/glassfish.zip
EXPOSE 8080 4848
CMD ["/opt/glassfish7/bin/asadmin", "start-domain", "--verbose"]
