FROM nginxinc/nginx-unprivileged:1.27.4-alpine
COPY web/nginx.conf /etc/nginx/conf.d/default.conf
COPY web/index.html /usr/share/nginx/html/index.html
COPY web/styles.css /usr/share/nginx/html/styles.css
COPY web/app.js /usr/share/nginx/html/app.js
EXPOSE 8080
