FROM php:8.3-fpm
# diagnosis only: no error_log and no access.log, so that php-fpm writes to stderr, which is the console of nanos
RUN sed -i -e '/^error_log/d' -e '/^access.log/d' /usr/local/etc/php-fpm.conf /usr/local/etc/php-fpm.d/*.conf && printf '[www]\nlisten = 0.0.0.0:9000\npm = static\npm.max_children = 1\n' > /usr/local/etc/php-fpm.d/zz-one.conf && cat /usr/local/etc/php-fpm.d/docker.conf /usr/local/etc/php-fpm.d/zz-docker.conf
COPY index.php /var/www/html/index.php
EXPOSE 9000
CMD ["php-fpm", "--nodaemonize"]
