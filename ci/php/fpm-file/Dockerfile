FROM php:8.3-fpm
RUN printf '[global]\nerror_log = /tmp/fpm.log\n[www]\nlisten = 0.0.0.0:9000\npm = static\npm.max_children = 1\naccess.log = /tmp/fpm-access.log\n' > /usr/local/etc/php-fpm.d/zz-one.conf
COPY index.php /var/www/html/index.php
EXPOSE 9000
CMD ["php-fpm", "--nodaemonize"]
