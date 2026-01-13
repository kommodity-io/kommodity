import { Toaster } from "sonner";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Routes, Route, useParams } from "react-router-dom";
import Index from "./pages/Index";
import NotFound from "./pages/NotFound";

const queryClient = new QueryClient();

const ClusterRoute = () => {
  const { clusterName } = useParams<{ clusterName: string }>();
  return <Index clusterName={clusterName || ""} subPath="cluster/" />;
};

const App = () => (
  <QueryClientProvider client={queryClient}>
    <Toaster />
    <BrowserRouter>
      <Routes>
        <Route path="/ui/cluster/:clusterName" element={<ClusterRoute />} />
        <Route path="/ui/kommodity" element={<Index clusterName="kommodity" subPath="/" />} />
        <Route path="*" element={<NotFound />} />
      </Routes>
    </BrowserRouter>
  </QueryClientProvider>
);

export default App;
