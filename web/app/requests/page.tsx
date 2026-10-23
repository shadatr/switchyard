import type { Metadata } from "next";
import { RequestsView } from "@/components/views/requests";

export const metadata: Metadata = { title: "Requests" };

export default function Page() {
  return <RequestsView />;
}
