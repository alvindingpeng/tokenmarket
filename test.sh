cd web
pnpm run build
cd ..
rm -rf static/out
mv web/out static/
go run . start