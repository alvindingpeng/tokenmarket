import "./globals.css";
import { ThemeProvider } from "@/provider/theme";
import { Toaster } from "@/components/ui/sonner"
import { NextIntlClientProvider } from 'next-intl';
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
        <meta name="apple-mobile-web-app-title" content="Switchyard" />
        <meta name="theme-color" content="#eae9e3" />
        <script
          async
          crossOrigin="anonymous"
          src="https://tweakcn.com/live-preview.min.js"
        />
      </head>
      <body className="antialiased">
        <ServiceWorkerRegister />
        <ThemeProvider attribute="class" defaultTheme="system" enableSystem>
          <QueryProvider>
            <NextIntlClientProvider>
              {children}
              <Toaster />
            </NextIntlClientProvider>
          </QueryProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}
