import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import "./globals.css";

const geistSans = Geist({ variable: "--font-geist-sans", subsets: ["latin"] });
const geistMono = Geist_Mono({ variable: "--font-geist-mono", subsets: ["latin"] });

export const metadata: Metadata = {
  metadataBase: new URL("https://infiniteocean.net"),
  title: { default: "Infinite Ocean: Spyglass", template: "%s | Infinite Ocean" },
  description: "Spyglass is the operating system for businesses ready to turn knowledge, people, work, and governed AI agents into coordinated action.",
  openGraph: {
    type: "website",
    siteName: "Infinite Ocean",
    title: "Infinite Ocean: Spyglass",
    description: "See the whole business. Move what matters.",
    images: [{ url: "/og.png", width: 1536, height: 1024, alt: "Infinite Ocean: Spyglass — See the whole business. Move what matters." }],
  },
  twitter: { card: "summary_large_image", title: "Infinite Ocean: Spyglass", description: "See the whole business. Move what matters.", images: ["/og.png"] },
};

export const dynamic = "force-dynamic";

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <html lang="en"><body className={`${geistSans.variable} ${geistMono.variable}`}>{children}</body></html>;
}
