# Stage 1: build the WAR from the upstream project at a pinned commit.
FROM maven:3.9-eclipse-temurin-17 AS build
RUN apt-get update && apt-get install -y --no-install-recommends git \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
RUN git clone https://github.com/Jakarta-EE-Petclinic/petclinic-jakartaee.git . \
    && git checkout 242b5d79724ba4ce51168b713a814b47c3f6675b
# Not sure: the glassfish profile may need more than skipping tests.
RUN chmod +x ./mvnw && ./mvnw -B -q -Pglassfish -DskipTests package
# The pom has packaging war and no finalName, so the file is
# target/petclinic-jakartaee-<version>.war; copy it under a fixed name.
RUN cp "$(ls target/*.war | head -n 1)" /petclinic.war

# Stage 2: GlassFish 7.0.5 with the MySQL driver.
FROM eclipse-temurin:17-jre
RUN apt-get update && apt-get install -y --no-install-recommends unzip curl \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /opt
RUN curl -fL -o glassfish.zip https://download.eclipse.org/ee4j/glassfish/glassfish-7.0.5.zip \
    && unzip -t glassfish.zip > /dev/null \
    && unzip -q glassfish.zip \
    && rm glassfish.zip
ENV GLASSFISH_HOME=/opt/glassfish7
ENV PATH=$GLASSFISH_HOME/bin:$PATH
RUN curl -fL -o $GLASSFISH_HOME/glassfish/domains/domain1/lib/mysql-connector-j-8.4.0.jar \
    https://repo1.maven.org/maven2/com/mysql/mysql-connector-j/8.4.0/mysql-connector-j-8.4.0.jar
COPY --from=build /petclinic.war /opt/petclinic.war
COPY start.sh /start.sh
RUN chmod +x /start.sh
EXPOSE 8080
ENTRYPOINT ["/start.sh"]
