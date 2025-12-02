import "./globals.css";
import { ThemeProvider } from "@/provider/theme";
import { Toaster } from "@/components/ui/sonner"
import { LocaleProvider } from "@/provider/locale";
import QueryProvider from "@/provider/query";
import { ServiceWorkerRegister } from "@/components/sw-register";



export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html suppressHydrationWarning>
      <head>
        <meta name="theme-color" content="#eae9e3" />
        <meta name="application-name" content="Switchyard" />
        <meta name="apple-mobile-web-app-capable" content="yes" />
        <meta name="apple-mobile-web-app-status-bar-style" content="black" />
        <meta name="apple-mobile-web-app-title" content="Switchyard" />
        <meta name="mobile-web-app-capable" content="yes" />
        <meta name="mobile-web-app-status-bar-style" content="black" />
        <meta name="mobile-web-app-title" content="Switchyard" />
        <link rel="manifest" href="/manifest.json" />
        <title>Switchyard</title>
        <script async crossOrigin="anonymous" src="https://tweakcn.com/live-preview.min.js" />
      </head>
      <body className="antialiased">
        <ServiceWorkerRegister />
        <ThemeProvider attribute="class" defaultTheme="system" enableSystem>
          <QueryProvider>
            <LocaleProvider>
              {children}
              <Toaster />
            </LocaleProvider>
          </QueryProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}
