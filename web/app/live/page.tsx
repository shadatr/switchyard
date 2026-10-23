import type { Metadata } from "next";
import { LiveView } from "@/components/views/live";

export const metadata: Metadata = { title: "Live" };

export default function Page() {
  return <LiveView />;
}
