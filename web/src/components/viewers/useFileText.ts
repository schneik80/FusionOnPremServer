// Fetches an uploaded file's bytes as text for the code and markdown viewers.
// tooLarge comes back when the server caps the file or it exceeds the client
// limit (see api.fileText), so the caller shows a download fallback instead.

import { useEffect, useState } from 'react'
import { api } from '../../api/client'
import i18n from '../../i18n'

export interface FileText {
  loading: boolean
  text: string
  tooLarge: boolean
  error?: string
}

export function useFileText(dmProjectId: string, itemId: string): FileText {
  const [state, setState] = useState<FileText>({ loading: true, text: '', tooLarge: false })
  useEffect(() => {
    let cancelled = false
    setState({ loading: true, text: '', tooLarge: false })
    api
      .fileText(dmProjectId, itemId)
      .then((r) => {
        if (!cancelled) setState({ loading: false, text: r.text, tooLarge: r.tooLarge })
      })
      .catch((e) => {
        if (!cancelled) {
          setState({
            loading: false,
            text: '',
            tooLarge: false,
            error: e instanceof Error ? e.message : i18n.t('browse:viewer.loadFailed'),
          })
        }
      })
    return () => {
      cancelled = true
    }
  }, [dmProjectId, itemId])
  return state
}
