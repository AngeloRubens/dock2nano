FROM php:8.3-apache
COPY index.php /var/www/html/index.php
# diagnosis only: the error log of the image is /proc/self/fd/2, which nanos does not have
RUN sed -i 's#/proc/self/fd/2#/dev/stderr#g' /etc/apache2/apache2.conf /etc/apache2/conf-available/*.conf && printf 'ServerName localhost\nLogLevel debug\n' > /etc/apache2/conf-enabled/zz-diag.conf
EXPOSE 80
